package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"wzstudy/internal/agentbundle"
	"wzstudy/internal/study"
)

// agentSecret returns the readable key for an identity in the signed-in user's
// workspace. The key is stored alongside its hash so it can be viewed again for
// redeployment; it is never included in bulk agent lists.
func (s *Server) agentSecret(w http.ResponseWriter, r *http.Request) {
	key, err := s.service.AgentSecret(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if key == "" {
		failure(w, http.StatusConflict, "agent_secret_unavailable", "该身份创建于旧版本，无法恢复明文密钥；请轮换密钥后再查看")
		return
	}
	profile, err := s.service.AgentPackageProfile(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{"access_key": key, "package": profile})
}

// agentPackage builds a ready-to-run archive for an existing identity. The
// caller must present the one-time key for that identity; the key and model
// credentials only exist in the returned ZIP and are never stored server-side.
func (s *Server) agentPackage(w http.ResponseWriter, r *http.Request) {
	if !agentbundle.Available() {
		failure(w, http.StatusServiceUnavailable, "agent_not_bundled", "当前服务端构建未内置采集端程序，请使用包含 agent 的完整发布包")
		return
	}
	var in struct {
		AccessKey string          `json:"access_key"`
		Model     deploymentModel `json:"model"`
	}
	if !decode(w, r, &in) {
		return
	}
	accessKey := strings.TrimSpace(in.AccessKey)
	agent, err := s.service.VerifyAgentKey(r.Context(), chi.URLParam(r, "id"), accessKey)
	if err != nil {
		s.fail(w, err)
		return
	}
	model, err := resolveDeploymentModel(in.Model)
	if err != nil {
		s.fail(w, err)
		return
	}
	archive, _, err := agentbundle.Build(requestOrigin(r), accessKey, agent.Name, model)
	if err != nil {
		s.fail(w, err)
		return
	}
	provider := strings.ToLower(strings.TrimSpace(in.Model.Provider))
	if provider == "" {
		provider = "deepseek"
	}
	if err := s.service.RememberAgentPackage(r.Context(), agent.ID, study.AgentPackageProfile{
		Provider: provider, Model: model.Model, BaseURL: model.BaseURL,
	}); err != nil {
		s.logger.Warn("remember agent package profile", "agent_id", agent.ID, "error", err)
	}
	filename := "wzstudy-agent-" + agent.Slug + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(archive)
}
