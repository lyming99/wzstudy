package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"wzstudy/internal/study"
)

type modelListRequest struct {
	Provider    string `json:"provider"`
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	UseSavedKey bool   `json:"use_saved_key"`
}

func resolveModelEndpoint(in modelListRequest) (string, string, error) {
	provider := strings.ToLower(strings.TrimSpace(in.Provider))
	if provider == "" {
		provider = "deepseek"
	}
	preset, known := deploymentPresets[provider]
	if !known && provider != "custom" {
		return "", "", study.ErrInvalid
	}
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = preset.BaseURL
	}
	apiKey := strings.TrimSpace(in.APIKey)
	if baseURL == "" || len(baseURL) > 500 || len(apiKey) > 500 || strings.ContainsAny(apiKey, "\r\n\x00") {
		return "", "", study.ErrInvalid
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", "", study.ErrInvalid
	}
	return strings.TrimRight(baseURL, "/"), apiKey, nil
}

// adminAgentModels asks an OpenAI-compatible endpoint for its model catalog so
// the deployment dialog can offer a picker while still accepting manual input.
func (s *Server) adminAgentModels(w http.ResponseWriter, r *http.Request) {
	var in modelListRequest
	if !decode(w, r, &in) {
		return
	}
	if in.APIKey == "" && in.UseSavedKey && s.runtime != nil {
		in.APIKey = s.runtime.SavedModelAPIKey()
	}
	baseURL, apiKey, err := resolveModelEndpoint(in)
	if err != nil {
		s.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		failure(w, 502, "model_list_failed", "模型地址无效")
		return
	}
	request.Header.Set("Accept", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 12 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		failure(w, 502, "model_list_failed", "无法连接模型服务，请检查地址与网络")
		return
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		failure(w, 502, "model_list_failed", "读取模型列表失败")
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure(w, 502, "model_list_failed", "模型服务返回 HTTP "+strconv.Itoa(response.StatusCode)+"，请检查 API Key 与地址")
		return
	}
	var decoded struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		failure(w, 502, "model_list_failed", "模型服务返回了无法识别的 JSON")
		return
	}
	seen := map[string]bool{}
	models := make([]string, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	sort.Strings(models)
	if len(models) > 200 {
		models = models[:200]
	}
	success(w, 200, map[string]any{"models": models, "base_url": baseURL})
}
