package collect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"wzstudy-agent/internal/serverapi"
	"wzstudy-agent/internal/tool"
)

const (
	maximumSubtitleTracks = 8
	maximumSubtitleBytes  = 256 << 10
)

var subtitleTiming = regexp.MustCompile(`(?m)^[\t ]*(?:[0-9]{2,}:)?[0-9]{2}:[0-9]{2}[,.][0-9]{3}[\t ]+-->[\t ]+(?:[0-9]{2,}:)?[0-9]{2}:[0-9]{2}[,.][0-9]{3}`)

// SubtitleCollector prepares optional subtitle text for one task execution.
// References never expose another task's text or grant local filesystem access.
// Fetching alone does not upload anything; Ingestor sends only selected tracks.
type SubtitleCollector struct {
	client       *http.Client
	allowDomains []string
	mu           sync.Mutex
	tracks       map[string]serverapi.ContentSubtitle
}

func NewSubtitleCollector(allowPrivate bool, allowDomains []string) *SubtitleCollector {
	collector := &SubtitleCollector{
		client:       newHTTPClient(allowPrivate),
		allowDomains: append([]string(nil), allowDomains...),
		tracks:       map[string]serverapi.ContentSubtitle{},
	}
	checkRedirect := collector.client.CheckRedirect
	collector.client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if err := checkRedirect(request, via); err != nil {
			return err
		}
		return collector.checkDomain(request.URL)
	}
	return collector
}

func (s *SubtitleCollector) Definition() tool.Definition {
	return tool.Definition{
		Name:        "fetch_subtitle",
		Description: "按需获取公开的 SRT/WebVTT 字幕文件地址（不是视频页面），保留完整时间轴并返回本任务 subtitle_ref，不回显整段正文。此步骤仅准备字幕；在 ingest_items.subtitles 中引用 ref 才会上传并关联音视频。字幕可选，由你按任务价值、可访问性和预算决定是否使用；没有字幕或获取失败可跳过，不影响媒体入库。不得生成或编造字幕。",
		Parameters: tool.Object(map[string]any{
			"url":    tool.String("真实可访问的 http(s) SRT/WebVTT 字幕地址；仍受公网和任务允许域名限制"),
			"format": map[string]any{"type": "string", "enum": []string{"srt", "vtt"}, "description": "可省略，按地址、Content-Type 和正文识别"},
		}, "url"),
	}
}

func (s *SubtitleCollector) Handle(ctx context.Context, call tool.Call) (tool.Result, error) {
	var args struct {
		URL    string `json:"url"`
		Format string `json:"format"`
	}
	if err := tool.DecodeArguments(call, &args); err != nil {
		return tool.Result{}, err
	}
	target, err := validateTarget(args.URL)
	if err != nil {
		return tool.Result{}, err
	}
	if err := s.checkDomain(target); err != nil {
		return tool.Result{}, err
	}
	format := strings.ToLower(strings.TrimSpace(args.Format))
	if format != "" && format != "srt" && format != "vtt" {
		return tool.Result{}, errors.New("字幕格式只支持 srt 或 vtt")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return tool.Result{}, err
	}
	request.Header.Set("User-Agent", "wzstudy-agent/0.1 (+learning-collector)")
	request.Header.Set("Accept", "text/vtt,application/x-subrip,text/plain;q=0.9,*/*;q=0.5")
	response, err := s.client.Do(request)
	if err != nil {
		return tool.Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNoContent {
		encoded, _ := json.Marshal(map[string]any{"available": false, "optional": true, "status": response.StatusCode, "message": "未获得可访问的字幕，可省略字幕继续音视频入库"})
		return tool.Result{Content: string(encoded), Summary: "字幕不可用，可跳过"}, nil
	}
	if response.StatusCode != http.StatusOK {
		return tool.Result{}, fmt.Errorf("字幕地址返回 HTTP %d，可省略字幕继续媒体任务", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximumSubtitleBytes+1))
	if err != nil {
		return tool.Result{}, err
	}
	if len(raw) > maximumSubtitleBytes {
		return tool.Result{}, errors.New("字幕超过单轨 256 KiB 限制，未截断或上传；可以跳过此字幕")
	}
	if format == "" {
		switch strings.ToLower(path.Ext(response.Request.URL.Path)) {
		case ".srt":
			format = "srt"
		case ".vtt":
			format = "vtt"
		default:
			if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff")), "WEBVTT") || strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/vtt") {
				format = "vtt"
			} else {
				format = "srt"
			}
		}
	}
	text, err := normalizeSubtitleText(format, string(raw))
	if err != nil {
		return tool.Result{}, err
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return tool.Result{}, err
	}
	ref := "subtitle_" + hex.EncodeToString(token[:])
	s.mu.Lock()
	s.tracks[ref] = serverapi.ContentSubtitle{Format: format, Content: text}
	s.mu.Unlock()
	encoded, err := json.Marshal(map[string]any{
		"available": true, "subtitle_ref": ref, "format": format, "size": len(text),
		"source_url": target.String(), "uploaded": false,
	})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: string(encoded), Summary: fmt.Sprintf("已准备 %s 字幕（%d 字节），由 AI 决定是否随资料上传", format, len(text)), Metadata: map[string]any{"subtitle_ref": ref, "size": len(text)}}, nil
}

func (s *SubtitleCollector) checkDomain(target *url.URL) error {
	if len(s.allowDomains) == 0 {
		return nil
	}
	host := strings.ToLower(target.Hostname())
	for _, domain := range s.allowDomains {
		domain = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(domain, ".")))
		if domain != "" && (host == domain || strings.HasSuffix(host, "."+domain)) {
			return nil
		}
	}
	return errors.New("字幕地址不属于本任务允许的域名")
}

func (s *SubtitleCollector) resolve(ref string) (serverapi.ContentSubtitle, bool) {
	if s == nil {
		return serverapi.ContentSubtitle{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	track, ok := s.tracks[ref]
	return track, ok
}

func normalizeSubtitleText(format, content string) (string, error) {
	if len(content) > maximumSubtitleBytes || !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 {
		return "", errors.New("字幕须为有效 UTF-8 文本，单轨不超过 256 KiB")
	}
	content = strings.TrimPrefix(content, "\ufeff")
	if strings.TrimSpace(content) == "" {
		return "", errors.New("字幕正文不能为空")
	}
	if format != "srt" && format != "vtt" {
		return "", errors.New("字幕格式只支持 srt 或 vtt")
	}
	vtt := strings.HasPrefix(strings.TrimSpace(content), "WEBVTT")
	if (format == "vtt" && !vtt) || (format == "srt" && vtt) || !subtitleTiming.MatchString(content) {
		return "", errors.New("字幕缺少有效的 SRT/WebVTT 时间轴，不能用摘要、网页或错误页冒充字幕")
	}
	return content, nil
}
