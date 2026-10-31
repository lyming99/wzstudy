package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// adminTestAgentModel makes a tiny, real inference request. It neither persists
// credentials nor returns upstream bodies (which may contain sensitive data).
func (s *Server) adminTestAgentModel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		modelListRequest
		Model       string `json:"model"`
		ClearAPIKey bool   `json:"clear_api_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.ClearAPIKey {
		in.APIKey = ""
	} else if strings.TrimSpace(in.APIKey) == "" && in.UseSavedKey && s.runtime != nil {
		in.APIKey = s.runtime.SavedModelAPIKey()
	}
	baseURL, key, err := resolveModelEndpoint(in.modelListRequest)
	model := strings.TrimSpace(in.Model)
	if err != nil || model == "" || len(model) > 120 || strings.ContainsAny(model, "\r\n\x00") {
		failure(w, 400, "invalid_model", "请填写有效的模型名称、Base URL 与 API Key")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"model": model, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}},
		"max_tokens": 16, "stream": false,
	})
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		failure(w, 400, "invalid_model", "模型地址无效")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	// Never forward the key to a redirected origin, including 307/308 POSTs.
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	started := time.Now()
	response, err := client.Do(req)
	if err != nil {
		failure(w, 502, "model_connection_failed", "无法连接模型服务或请求超时，请检查 Base URL 与网络")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := "模型服务返回 HTTP " + strconv.Itoa(response.StatusCode)
		switch response.StatusCode {
		case 401, 403:
			message += "：模型 API Key 无效或无访问权限（不是采集端身份密钥）"
		case 404:
			message += "：请检查 Base URL 与模型名称"
		case 429:
			message += "：请求受限或余额不足，请检查模型账户"
		default:
			message += "：请检查模型服务与配置"
		}
		failure(w, 502, "model_connection_failed", message)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	var result struct {
		Choices []struct {
			Message map[string]json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &result) != nil || len(result.Choices) == 0 || len(result.Choices[0].Message) == 0 {
		failure(w, 502, "model_connection_failed", "模型服务未返回有效的对话响应，请检查地址与模型兼容性")
		return
	}
	success(w, 200, map[string]any{"connected": true, "model": model, "latency_ms": time.Since(started).Milliseconds(), "message": "模型连接测试成功"})
}
