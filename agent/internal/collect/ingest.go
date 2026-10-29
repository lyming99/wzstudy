package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"wzstudy-agent/internal/serverapi"
	"wzstudy-agent/internal/tool"
)

// Ingestor owns the task-scoped push budget. The server enforces the same quota
// again, so a compromised agent cannot exceed it.
type Ingestor struct {
	mu      sync.Mutex
	client  *serverapi.Client
	task    *serverapi.Task
	attempt int
	stats   *serverapi.Stats
}

func NewIngestor(client *serverapi.Client, task *serverapi.Task, attempt int, stats *serverapi.Stats) *Ingestor {
	return &Ingestor{client: client, task: task, attempt: attempt, stats: stats}
}

func (i *Ingestor) Stats() serverapi.Stats {
	i.mu.Lock()
	defer i.mu.Unlock()
	return *i.stats
}

func (i *Ingestor) Definition() tool.Definition {
	content := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"external_id":  tool.String("稳定且唯一的条目标识；同一来源内重复会被更新而不是新建"),
			"title":        tool.String("真实标题，<=500 字节"),
			"summary":      tool.String("摘要，<=8000 字节"),
			"content":      tool.String("整理后的正文或摘要文本，<=262144 字节"),
			"format":       map[string]any{"type": "string", "enum": []string{"text", "markdown"}, "description": "正文格式，默认 text"},
			"url":          tool.String("原始出处链接；任务限定域名时必须属于允许域名"),
			"author":       tool.String("原作者"),
			"category":     tool.String("分类"),
			"language":     tool.String("语言代码，如 zh-CN"),
			"tags":         map[string]any{"type": "array", "maxItems": 20, "items": map[string]any{"type": "string", "maxLength": 80}},
			"published_at": map[string]any{"type": "string", "format": "date-time", "description": "原始发布时间，未知就省略"},
			"kind":         map[string]any{"type": "string", "enum": []string{"article", "audio", "video", "carousel"}},
		},
		"required":             []string{"external_id", "title"},
		"additionalProperties": false,
	}
	return tool.Definition{
		Name:        "ingest_items",
		Description: "把整理好的条目推送到服务端并写入任务绑定的来源。单次最多 20 条，整个任务受 max_items 配额限制；标题必须真实，正文用整理后的 Markdown/文本，不要整篇复制受版权保护的内容。",
		Parameters: tool.Object(map[string]any{
			"items": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": content},
		}, "items"),
	}
}

func (i *Ingestor) Handle(ctx context.Context, call tool.Call) (tool.Result, error) {
	var args struct {
		Items []serverapi.Content `json:"items"`
	}
	if err := tool.DecodeArguments(call, &args); err != nil {
		return tool.Result{}, err
	}
	if len(args.Items) == 0 || len(args.Items) > 20 {
		return tool.Result{}, fmt.Errorf("items 数量必须在 1-20 之间")
	}
	i.mu.Lock()
	used := i.stats.ItemsCreated + i.stats.ItemsUpdated + i.stats.ItemsUnchanged
	remaining := i.task.Constraints.MaxItems - used
	i.mu.Unlock()
	if remaining <= 0 {
		return tool.Result{Content: "任务条目配额已用完，请调用 finish_task 结束。", Summary: "quota reached", IsError: true}, nil
	}
	if len(args.Items) > remaining {
		return tool.Result{}, fmt.Errorf("本次推送 %d 条，但任务只剩 %d 条配额", len(args.Items), remaining)
	}
	receipt, err := i.client.PushItems(ctx, i.task.ID, i.attempt, args.Items)
	if err != nil {
		return tool.Result{}, err
	}
	i.mu.Lock()
	i.stats.ItemsCreated += receipt.Created
	i.stats.ItemsUpdated += receipt.Updated
	i.stats.ItemsUnchanged += receipt.Unchanged
	i.mu.Unlock()
	encoded, err := json.Marshal(map[string]any{
		"receipt_id": receipt.ID,
		"created":    receipt.Created,
		"updated":    receipt.Updated,
		"unchanged":  receipt.Unchanged,
		"published":  receipt.Published,
		"items":      receipt.Items,
		"quota_left": remaining - len(args.Items),
	})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(encoded),
		Summary: fmt.Sprintf("入库 %d 条（新增 %d，更新 %d）", len(args.Items), receipt.Created, receipt.Updated),
		Metadata: map[string]any{
			"created": receipt.Created, "updated": receipt.Updated, "unchanged": receipt.Unchanged,
		},
	}, nil
}
