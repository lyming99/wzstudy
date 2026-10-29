package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenAI is an OpenAI-compatible Chat Completions client with function calling.
// It also works with local gateways that implement /chat/completions + tools.
type OpenAI struct {
	BaseURL *url.URL
	APIKey  string
	Client  *http.Client
}

func NewOpenAI(baseURL, apiKey string) (*OpenAI, error) {
	parsed, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("invalid model base URL")
	}
	return &OpenAI{BaseURL: parsed, APIKey: apiKey, Client: &http.Client{Timeout: 120 * time.Second}}, nil
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Tools       []openAITool    `json:"tools,omitempty"`
	ToolChoice  string          `json:"tool_choice,omitempty"`
	Temperature float64         `json:"temperature"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (o *OpenAI) Chat(ctx context.Context, model string, messages []Message, tools []ToolSchema) (Response, error) {
	request := openAIRequest{Model: model, Messages: make([]openAIMessage, 0, len(messages)), Temperature: 0.2}
	for _, message := range messages {
		entry := openAIMessage{Role: string(message.Role), Content: message.Content, ToolCallID: message.ToolCallID}
		for _, call := range message.ToolCalls {
			arguments := call.Arguments
			if strings.TrimSpace(arguments) == "" {
				arguments = "{}"
			}
			entry.ToolCalls = append(entry.ToolCalls, openAIToolCall{ID: call.ID, Type: "function", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: call.Name, Arguments: arguments}})
		}
		request.Messages = append(request.Messages, entry)
	}
	for _, schema := range tools {
		definition := openAITool{Type: "function"}
		definition.Function.Name = schema.Name
		definition.Function.Description = schema.Description
		definition.Function.Parameters = schema.Parameters
		if definition.Function.Parameters == nil {
			definition.Function.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		request.Tools = append(request.Tools, definition)
	}
	if len(request.Tools) > 0 {
		request.ToolChoice = "auto"
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	endpoint := o.BaseURL.String() + "/chat/completions"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Response{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if o.APIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	httpResponse, err := o.Client.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("model request failed: %w", err)
	}
	defer httpResponse.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(httpResponse.Body, 4<<20))
	if err != nil {
		return Response{}, err
	}
	var decoded openAIResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Response{}, fmt.Errorf("model returned invalid JSON (HTTP %d)", httpResponse.StatusCode)
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		message := "model request rejected"
		if decoded.Error != nil && decoded.Error.Message != "" {
			message = decoded.Error.Message
		}
		return Response{}, fmt.Errorf("model HTTP %d: %s", httpResponse.StatusCode, truncate(message, 300))
	}
	if len(decoded.Choices) == 0 {
		return Response{}, errors.New("model returned no choices")
	}
	choice := decoded.Choices[0]
	out := Response{
		Text:         choice.Message.Content,
		FinishReason: choice.FinishReason,
		Usage:        Usage{PromptTokens: decoded.Usage.PromptTokens, CompletionTokens: decoded.Usage.CompletionTokens},
	}
	for _, call := range choice.Message.ToolCalls {
		if call.Function.Name == "" {
			continue
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
	}
	return out, nil
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
