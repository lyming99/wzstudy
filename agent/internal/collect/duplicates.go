package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"wzstudy-agent/internal/serverapi"
	"wzstudy-agent/internal/tool"
)

type DuplicateChecker struct {
	client *serverapi.Client
	task   *serverapi.Task
}

func NewDuplicateChecker(client *serverapi.Client, task *serverapi.Task) *DuplicateChecker {
	return &DuplicateChecker{client: client, task: task}
}
func (d *DuplicateChecker) Definition() tool.Definition {
	return tool.Definition{
		Name: "check_existing", Description: "下载、上传前对候选内容做查重（最多20条）。只返回候选是否已存在或已删除，不读取资料库内容。skip任务需跳过重复并补选，不得更换标识绕过；update任务可更新仍有效的同来源内容。",
		Parameters: tool.Object(map[string]any{"items": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": tool.Object(map[string]any{"external_id": tool.String("稳定平台内容ID，如BV号"), "url": tool.String("原始内容URL，B站必须提供完整视频链接")}, "external_id")}}, "items"),
	}
}
func (d *DuplicateChecker) Handle(ctx context.Context, call tool.Call) (tool.Result, error) {
	var in struct {
		Items []serverapi.CollectionIdentity `json:"items"`
	}
	if err := tool.DecodeArguments(call, &in); err != nil {
		return tool.Result{}, err
	}
	if len(in.Items) < 1 || len(in.Items) > 20 {
		return tool.Result{}, fmt.Errorf("查重候选必须为1-20条")
	}
	out, err := d.client.CheckDuplicates(ctx, d.task.ID, d.task.Attempts, in.Items)
	if err != nil {
		return tool.Result{}, err
	}
	raw, err := json.Marshal(map[string]any{"items": out})
	if err != nil {
		return tool.Result{}, err
	}
	n := 0
	for _, item := range out {
		if item.Exists {
			n++
		}
	}
	return tool.Result{Content: string(raw), Summary: fmt.Sprintf("查重 %d 条，已有或删除 %d 条", len(out), n)}, nil
}
