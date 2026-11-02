package collect

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"wzstudy-agent/internal/serverapi"
)

// These tool-only references must be resolved before calling the server. The
// server receives its existing inline subtitle contract, never collector tokens.
type subtitleInput struct {
	serverapi.ContentSubtitle
	Ref string `json:"ref,omitempty"`
}

type ingestItem struct {
	serverapi.Content
	Subtitles []subtitleInput `json:"subtitles,omitempty"`
}

func (i *Ingestor) prepareItems(input []ingestItem) ([]serverapi.Content, error) {
	items := make([]serverapi.Content, len(input))
	for index, item := range input {
		items[index] = item.Content
		if len(item.Subtitles) == 0 {
			continue
		}
		if len(item.Subtitles) > maximumSubtitleTracks {
			return nil, fmt.Errorf("第 %d 条资料的字幕超过 8 轨限制", index+1)
		}
		if item.Kind != "" && item.Kind != "video" && item.Kind != "audio" {
			return nil, fmt.Errorf("第 %d 条资料不是音频或视频，不能关联字幕", index+1)
		}
		ids := map[string]bool{}
		for _, inputTrack := range item.Subtitles {
			track := inputTrack.ContentSubtitle
			track.ID = strings.TrimSpace(track.ID)
			track.Language = strings.TrimSpace(track.Language)
			track.Format = strings.ToLower(strings.TrimSpace(track.Format))
			if track.ID == "" || len(track.ID) > 200 || !utf8.ValidString(track.ID) || track.Language == "" || len(track.Language) > 35 || !utf8.ValidString(track.Language) || ids[track.ID] {
				return nil, fmt.Errorf("第 %d 条资料的字幕 ID/语言无效或字幕 ID 重复", index+1)
			}
			if ref := strings.TrimSpace(inputTrack.Ref); ref != "" {
				if track.Content != "" {
					return nil, fmt.Errorf("字幕 %s 的 ref 与 content 不能同时提供", track.ID)
				}
				prepared, ok := i.subtitles.resolve(ref)
				if !ok {
					return nil, fmt.Errorf("字幕 %s 的 ref 无效或不属于本次任务；可重新获取或省略字幕", track.ID)
				}
				if track.Format != "" && track.Format != prepared.Format {
					return nil, fmt.Errorf("字幕 %s 的 format 与已获取字幕不一致", track.ID)
				}
				track.Format, track.Content = prepared.Format, prepared.Content
			}
			if track.Format == "" {
				track.Format = "srt"
			}
			content, err := normalizeSubtitleText(track.Format, track.Content)
			if err != nil {
				return nil, fmt.Errorf("字幕 %s：%w；可移除此字幕后继续媒体入库", track.ID, err)
			}
			track.Content = content
			ids[track.ID] = true
			items[index].Subtitles = append(items[index].Subtitles, track)
		}
	}
	return items, nil
}
