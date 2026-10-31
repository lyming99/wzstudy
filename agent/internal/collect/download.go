package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wzstudy-agent/internal/tool"
)

const defaultMediaBytes = 200 << 20

type Downloader struct {
	binary   string
	maxBytes int64
	logger   *slog.Logger
}

func NewDownloader(binary string, maxBytes int64, logger *slog.Logger) *Downloader {
	if maxBytes <= 0 {
		maxBytes = defaultMediaBytes
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Downloader{binary: strings.TrimSpace(binary), maxBytes: maxBytes, logger: logger}
}

func (d *Downloader) Definition() tool.Definition {
	return tool.Definition{
		Name:        "download_media",
		Description: "使用 yt-dlp 下载视频或音频到本地临时目录，返回本地文件路径、标题和时长。视频/音频任务入库前必须先用此工具下载，再用 upload_asset 上传，并在 ingest_items 的 assets 中引用返回的资产 ID。",
		Parameters: tool.Object(map[string]any{
			"url":       tool.String("视频或音频页面 URL，必须是 http(s) 地址"),
			"format":    map[string]any{"type": "string", "enum": []string{"video", "audio"}, "description": "下载类型，默认 video"},
			"max_bytes": tool.Integer("最大文件字节数，默认 200MB", 1<<20, 1<<30),
		}, "url"),
	}
}

type downloadOutput struct {
	Path       string  `json:"path"`
	Title      string  `json:"title,omitempty"`
	Duration   float64 `json:"duration,omitempty"`
	WebpageURL string  `json:"webpage_url,omitempty"`
	Extractor  string  `json:"extractor,omitempty"`
	Size       int64   `json:"size"`
	Format     string  `json:"format"`
}

func (d *Downloader) Handle(ctx context.Context, call tool.Call) (tool.Result, error) {
	var args struct {
		URL      string `json:"url"`
		Format   string `json:"format"`
		MaxBytes int64  `json:"max_bytes"`
	}
	if err := tool.DecodeArguments(call, &args); err != nil {
		return tool.Result{}, err
	}
	parsed, err := url.Parse(strings.TrimSpace(args.URL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return tool.Result{}, errors.New("url 必须是绝对 http(s) 地址")
	}
	format := strings.ToLower(strings.TrimSpace(args.Format))
	if format == "" {
		format = "video"
	}
	if format != "video" && format != "audio" {
		return tool.Result{}, errors.New("format 只能是 video 或 audio")
	}
	maxBytes := args.MaxBytes
	if maxBytes <= 0 || maxBytes > d.maxBytes {
		maxBytes = d.maxBytes
	}
	binary, err := d.resolveBinary()
	if err != nil {
		return tool.Result{}, err
	}
	directory, err := os.MkdirTemp("", "wzstudy-media-")
	if err != nil {
		return tool.Result{}, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }

	metaCtx, cancelMeta := context.WithTimeout(ctx, 90*time.Second)
	metaOutput, metaErr := runLimited(metaCtx, binary, []string{"--no-playlist", "--no-warnings", "-J", "--", args.URL}, 2<<20)
	cancelMeta()
	var metadata struct {
		Title      string  `json:"title"`
		Duration   float64 `json:"duration"`
		WebpageURL string  `json:"webpage_url"`
		Extractor  string  `json:"extractor"`
	}
	if metaErr == nil {
		_ = json.Unmarshal([]byte(metaOutput), &metadata)
	}

	outputTemplate := filepath.Join(directory, "%(title).80s.%(ext)s")
	command := []string{
		"--no-playlist", "--no-progress", "--no-warnings",
		"--max-filesize", strconv.FormatInt(maxBytes, 10),
	}
	if format == "audio" {
		command = append(command, "-x", "--audio-format", "mp3")
	} else {
		command = append(command, "-f", "bv*+ba/b", "--merge-output-format", "mp4")
	}
	command = append(command, "-o", outputTemplate, "--", args.URL)
	downloadCtx, cancelDownload := context.WithTimeout(ctx, 5*time.Minute)
	output, downloadErr := runLimited(downloadCtx, binary, command, 64<<10)
	cancelDownload()
	if downloadErr != nil {
		cleanup()
		message := strings.TrimSpace(output)
		if message == "" {
			message = downloadErr.Error()
		}
		if len(message) > 1500 {
			message = message[:1500]
		}
		return tool.Result{}, fmt.Errorf("yt-dlp 下载失败：%s", message)
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		cleanup()
		return tool.Result{}, err
	}
	var selected os.DirEntry
	var selectedSize int64
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(strings.ToLower(entry.Name()), ".part") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > selectedSize {
			selected = entry
			selectedSize = info.Size()
		}
	}
	if selected == nil || selectedSize == 0 {
		cleanup()
		return tool.Result{}, errors.New("yt-dlp 未产生可用的媒体文件")
	}
	if selectedSize > maxBytes {
		cleanup()
		return tool.Result{}, fmt.Errorf("下载文件超过大小限制（%d 字节 > %d）", selectedSize, maxBytes)
	}
	result := downloadOutput{
		Path:       filepath.Join(directory, selected.Name()),
		Title:      metadata.Title,
		Duration:   metadata.Duration,
		WebpageURL: metadata.WebpageURL,
		Extractor:  metadata.Extractor,
		Size:       selectedSize,
		Format:     format,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		cleanup()
		return tool.Result{}, err
	}
	d.logger.Info("media downloaded", "path", result.Path, "size", result.Size, "format", format)
	return tool.Result{
		Content:  string(encoded),
		Summary:  fmt.Sprintf("下载 %s：%d 字节", format, result.Size),
		Metadata: map[string]any{"path": result.Path, "size": result.Size},
	}, nil
}

func (d *Downloader) resolveBinary() (string, error) {
	if d.binary != "" {
		if stat, err := os.Stat(d.binary); err == nil && stat.Mode().IsRegular() {
			return d.binary, nil
		}
	}
	for _, name := range []string{"yt-dlp", "yt-dlp.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
		if cwd, err := os.Getwd(); err == nil {
			candidate := filepath.Join(cwd, name)
			if stat, err := os.Stat(candidate); err == nil && stat.Mode().IsRegular() {
				return candidate, nil
			}
		}
	}
	return "", errors.New("未找到 yt-dlp：请安装并加入 PATH，或设置 AGENT_YTDLP_PATH 指向可执行文件")
}

type limitedWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	if w.limit <= 0 {
		w.limit = 64 << 10
	}
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			_, _ = w.buffer.Write(data[:remaining])
		} else {
			_, _ = w.buffer.Write(data)
		}
	}
	return len(data), nil
}

func runLimited(ctx context.Context, binary string, args []string, limit int) (string, error) {
	command := exec.CommandContext(ctx, binary, args...)
	writer := &limitedWriter{limit: limit}
	command.Stdout = writer
	command.Stderr = writer
	err := command.Run()
	return writer.buffer.String(), err
}
