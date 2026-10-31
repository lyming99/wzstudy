package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wzstudy-agent/internal/serverapi"
	"wzstudy-agent/internal/tool"
)

type Uploader struct {
	client *serverapi.Client
}

func NewUploader(client *serverapi.Client) *Uploader {
	return &Uploader{client: client}
}

func (u *Uploader) Definition() tool.Definition {
	return tool.Definition{
		Name:        "upload_asset",
		Description: "把 download_media 下载的媒体文件上传到服务端，返回资产 ID。随后在 ingest_items 的 assets 中引用该 ID；视频/音频任务没有对应资产会被服务端拒绝入库。",
		Parameters: tool.Object(map[string]any{
			"path": tool.String("download_media 返回的本地文件绝对路径"),
		}, "path"),
	}
}

func (u *Uploader) Handle(ctx context.Context, call tool.Call) (tool.Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := tool.DecodeArguments(call, &args); err != nil {
		return tool.Result{}, err
	}
	path := strings.TrimSpace(args.Path)
	if path == "" || !filepath.IsAbs(path) {
		return tool.Result{}, errors.New("path 必须是 download_media 返回的绝对路径")
	}
	cleaned := filepath.Clean(path)
	prefix := filepath.Join(os.TempDir(), "wzstudy-media-")
	if !strings.HasPrefix(strings.ToLower(cleaned), strings.ToLower(prefix)) {
		return tool.Result{}, errors.New("只能上传 download_media 生成的临时媒体文件")
	}
	info, err := os.Stat(cleaned)
	if err != nil || !info.Mode().IsRegular() {
		return tool.Result{}, errors.New("媒体文件不存在或不是普通文件")
	}
	if info.Size() <= 0 || info.Size() > 1<<30 {
		return tool.Result{}, errors.New("媒体文件大小超出限制")
	}
	asset, err := u.client.UploadAsset(ctx, cleaned)
	if err != nil {
		return tool.Result{}, err
	}
	encoded, err := json.Marshal(asset)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content:  string(encoded),
		Summary:  fmt.Sprintf("上传资产 %s（%d 字节）", asset.ID, asset.Size),
		Metadata: map[string]any{"asset_id": asset.ID, "size": asset.Size},
	}, nil
}
