package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wzstudy/internal/study"
)

const agentTestSRT = "1\n00:00:00,000 --> 00:00:02,000\n真实中文字幕\n"
const agentTestVTT = "WEBVTT\n\n00:00.000 --> 00:02.000\nEnglish subtitles\n"

func agentSubtitleFixture(t *testing.T, kind string) (*study.Service, http.Handler, string, study.CollectionTask, study.Asset) {
	t.Helper()
	service, handler, source, agent, key, oldTask := agentApp(t)
	ctx := testWorkspaceContext()
	if _, err := service.CancelAgentTask(ctx, oldTask.ID, "replace article fixture with media task"); err != nil {
		t.Fatal(err)
	}
	asset := study.Asset{ID: uuid.NewString(), Name: "media.mp4", MIME: "video/mp4", Size: 128, CreatedAt: service.Now()}
	if kind == "audio" {
		asset.Name, asset.MIME = "media.wav", "audio/wav"
	}
	if err := service.RegisterUploadedAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateCollectionTask(ctx, study.CollectionTaskInput{
		SourceID: source.ID, Goal: "采集媒体，字幕由 AI 自主决定是否上传", AgentID: agent.ID,
		Constraints: study.TaskConstraints{ContentType: kind, MaxItems: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	var claimed struct {
		Task study.CollectionTask `json:"task"`
	}
	data(t, request(t, handler, http.MethodPost, "/api/v1/agent/tasks/claim", key, map[string]any{}), &claimed)
	if claimed.Task.ID != task.ID {
		t.Fatalf("claimed wrong task: %+v", claimed)
	}
	return service, handler, key, claimed.Task, asset
}

func TestAgentOptionalSubtitleHTTPRoundTrip(t *testing.T) {
	for _, kind := range []string{"video", "audio"} {
		for _, withSubtitles := range []bool{false, true} {
			name := kind + "_without_subtitles"
			if withSubtitles {
				name = kind + "_with_srt_and_vtt"
			}
			t.Run(name, func(t *testing.T) {
				service, handler, key, task, asset := agentSubtitleFixture(t, kind)
				item := study.Content{Kind: kind, ExternalID: "media-1", Title: "真实音视频", Assets: []study.Asset{{ID: asset.ID}}}
				if withSubtitles {
					item.Subtitles = []study.ContentSubtitle{
						{ID: "zh", Language: "zh-CN", Format: "srt", Content: agentTestSRT},
						{ID: "en", Language: "en", Format: "vtt", Content: agentTestVTT},
					}
				}
				var receipt study.IntakeReceipt
				data(t, request(t, handler, http.MethodPost, "/api/v1/agent/tasks/"+task.ID+"/items", key,
					map[string]any{"attempt": task.Attempts, "items": []study.Content{item}}), &receipt)
				if receipt.Created != 1 || len(receipt.Items) != 1 || receipt.TaskID != task.ID {
					t.Fatalf("unexpected receipt: %+v", receipt)
				}
				stored, err := service.Item(testWorkspaceContext(), receipt.Items[0].ID)
				if err != nil || stored.SourceID != task.SourceID || len(stored.Draft.Assets) != 1 || stored.Draft.Assets[0].ID != asset.ID {
					t.Fatalf("subtitle upload changed media/source binding: %+v %v", stored, err)
				}
				tracks := stored.Draft.Subtitles
				if withSubtitles {
					if len(tracks) != 2 || tracks[0].Content != agentTestSRT || tracks[1].Content != agentTestVTT || tracks[0].Language != "zh-CN" || tracks[1].Format != "vtt" {
						t.Fatalf("subtitle text or metadata not persisted: %+v", tracks)
					}
				} else if len(tracks) != 0 {
					t.Fatal("missing subtitles must not be synthesized or required")
				}
			})
		}
	}
}

func TestAgentInvalidSubtitleBatchIsAtomicAndCanRetryWithoutSubtitles(t *testing.T) {
	base := study.ContentSubtitle{ID: "zh", Language: "zh-CN", Format: "srt", Content: agentTestSRT}
	for _, name := range []string{"too_many", "duplicate_id", "oversize", "unsupported_format", "missing_language", "wrong_kind"} {
		t.Run(name, func(t *testing.T) {
			service, handler, key, task, asset := agentSubtitleFixture(t, "video")
			good := study.Content{Kind: "video", ExternalID: "good", Title: "Good video", Assets: []study.Asset{{ID: asset.ID}}}
			bad := good
			bad.ExternalID = "bad"
			bad.Subtitles = []study.ContentSubtitle{base}
			switch name {
			case "too_many":
				for i := 0; i < 8; i++ {
					bad.Subtitles = append(bad.Subtitles, base)
				}
			case "duplicate_id":
				bad.Subtitles = append(bad.Subtitles, base)
			case "oversize":
				bad.Subtitles[0].Content = strings.Repeat("x", 262145)
			case "unsupported_format":
				bad.Subtitles[0].Format = "ass"
			case "missing_language":
				bad.Subtitles[0].Language = ""
			case "wrong_kind":
				bad.Kind = "article"
			}
			path := "/api/v1/agent/tasks/" + task.ID + "/items"
			response := request(t, handler, http.MethodPost, path, key, map[string]any{"attempt": task.Attempts, "items": []study.Content{good, bad}})
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid subtitle should be rejected: %d %s", response.Code, response.Body.String())
			}
			current, err := service.CollectionTask(testWorkspaceContext(), task.ID)
			if err != nil || current.Stats.ItemsCreated != 0 {
				t.Fatalf("failed subtitle batch consumed quota: %+v %v", current, err)
			}
			var receipt study.IntakeReceipt
			data(t, request(t, handler, http.MethodPost, path, key, map[string]any{"attempt": task.Attempts, "items": []study.Content{good}}), &receipt)
			if receipt.Created != 1 || receipt.Skipped != 0 {
				t.Fatalf("partial write or retry without optional subtitles failed: %+v", receipt)
			}
		})
	}
}
