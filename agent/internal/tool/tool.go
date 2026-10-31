package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

type Definition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type Call struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type Result struct {
	Content  string
	Summary  string
	IsError  bool
	Metadata map[string]any
}

type Handler func(context.Context, Call) (Result, error)

// FinishControl is shared between the finish_task tool and the loop so an
// explicit finish can end the run without another model round.
type FinishControl struct {
	Done    bool
	Summary string
}

type Registry struct {
	definitions []Definition
	handlers    map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: map[string]Handler{}}
}

func (r *Registry) Register(definition Definition, handler Handler) {
	r.definitions = append(r.definitions, definition)
	r.handlers[definition.Name] = handler
}

func (r *Registry) Definitions() []Definition {
	return r.definitions
}

// Execute runs one tool call and converts a handler failure or panic into an
// error result so the model can recover instead of the run crashing.
func (r *Registry) Execute(ctx context.Context, call Call) (result Result) {
	handler, ok := r.handlers[call.Name]
	if !ok {
		return Result{Content: "未知工具：" + call.Name, Summary: "unknown tool", IsError: true}
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result = Result{Content: "工具执行异常，请换一种方式。", Summary: "tool panic", IsError: true}
		}
	}()
	result, err := handler(ctx, call)
	if err != nil {
		message := err.Error()
		if len(message) > 500 {
			message = message[:500]
			for len(message) > 0 && !utf8.ValidString(message) {
				message = message[:len(message)-1]
			}
		}
		return Result{Content: "工具执行失败：" + message, Summary: "tool error", IsError: true}
	}
	if result.Content == "" {
		result.Content = "(无内容)"
	}
	return result
}

func DecodeArguments(call Call, target any) error {
	if len(call.Arguments) == 0 {
		return nil
	}
	if err := json.Unmarshal(call.Arguments, target); err != nil {
		return fmt.Errorf("参数不是有效 JSON: %w", err)
	}
	return nil
}

func Object(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func String(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func Integer(description string, minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "description": description, "minimum": minimum, "maximum": maximum}
}

func Array(items map[string]any, description string) map[string]any {
	return map[string]any{"type": "array", "items": items, "description": description}
}
