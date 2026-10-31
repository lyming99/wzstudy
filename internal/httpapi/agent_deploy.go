package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"wzstudy/internal/agentbundle"
	"wzstudy/internal/study"
)

type deploymentModel struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
}

var deploymentPresets = map[string]agentbundle.ModelConfig{
	"deepseek": {BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-chat"},
	"openai":   {BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini"},
	"qwen":     {BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", Model: "qwen-plus"},
}

func resolveDeploymentModel(in deploymentModel) (agentbundle.ModelConfig, error) {
	provider := strings.ToLower(strings.TrimSpace(in.Provider))
	if provider == "" {
		provider = "deepseek"
	}
	preset, known := deploymentPresets[provider]
	if !known && provider != "custom" {
		return agentbundle.ModelConfig{}, study.ErrInvalid
	}
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = preset.BaseURL
	}
	model := strings.TrimSpace(in.Model)
	if model == "" {
		model = preset.Model
	}
	apiKey := strings.TrimSpace(in.APIKey)
	if baseURL == "" || len(baseURL) > 500 || model == "" || len(model) > 120 || len(apiKey) > 500 || strings.ContainsAny(apiKey, "\r\n\x00") {
		return agentbundle.ModelConfig{}, study.ErrInvalid
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return agentbundle.ModelConfig{}, study.ErrInvalid
	}
	return agentbundle.ModelConfig{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, Model: model}, nil
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); forwarded == "https" || forwarded == "http" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host
}

func (s *Server) adminAgentBinary(w http.ResponseWriter, r *http.Request) {
	data, err := agentbundle.Binary()
	if err != nil {
		failure(w, http.StatusServiceUnavailable, "agent_not_bundled", "当前服务端构建未内置采集端程序，请使用完整发布包")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="wzstudy-agent.exe"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// adminCreateAgentDeployment creates a collection agent identity and returns a
// ready-to-run ZIP. The key is generated once, embedded only in that archive and
// never stored in plaintext.
func (s *Server) adminCreateAgentDeployment(w http.ResponseWriter, r *http.Request) {
	if !agentbundle.Available() {
		failure(w, http.StatusServiceUnavailable, "agent_not_bundled", "当前服务端构建未内置采集端程序，请使用完整发布包")
		return
	}
	var in struct {
		Name  string          `json:"name"`
		Slug  string          `json:"slug"`
		Model deploymentModel `json:"model"`
	}
	if !decode(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "采集端 " + time.Now().Format("2006-01-02 15:04")
	}
	if len(name) > 120 {
		s.fail(w, study.ErrInvalid)
		return
	}
	modelConfig, err := resolveDeploymentModel(in.Model)
	if err != nil {
		s.fail(w, err)
		return
	}
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = "deploy-" + strings.ToLower(uuid.NewString()[:8])
	}
	_, key, err := s.service.CreateAgent(r.Context(), study.AgentInput{Slug: slug, Name: name, Description: "由一键部署生成"})
	if err != nil {
		s.fail(w, err)
		return
	}
	archive, _, err := agentbundle.Build(requestOrigin(r), key, name, modelConfig)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="wzstudy-agent.zip"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(archive)
}
