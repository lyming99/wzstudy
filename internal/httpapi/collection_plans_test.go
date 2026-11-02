package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"wzstudy/internal/config"
	"wzstudy/internal/study"
)

func collectionApp(t *testing.T) (*study.Service, http.Handler) {
	t.Helper()
	s, _, dir := managementApp(t, 1<<20)
	h := New(config.Config{Env: "test", Store: "memory", AdminKey: adminKey, ReadKey: legacyReadKey, DataDir: dir, AgentEnabled: true}, s, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return s, h
}
func collectionBody(kind string) map[string]any {
	return map[string]any{"name": "采集计划", "enabled": true, "task": map[string]any{"goal": "采集官方文章", "constraints": map[string]any{"max_items": 3, "allow_domains": []string{"go.dev"}}}, "schedule": map[string]any{"kind": kind, "timezone": "UTC"}}
}
func TestWorkspaceCollectionPlanLifecycle(t *testing.T) {
	_, h := collectionApp(t)
	_, alice := managedLearner(t, h, "collection-alice")
	_, bob := managedLearner(t, h, "collection-bob")
	base := "/api/v1/workspace/agent/collection-plans"
	var p study.CollectionPlan
	data(t, request(t, h, "POST", base, alice, collectionBody("manual")), &p)
	if p.ID == "" || p.Task.SourceID == "" {
		t.Fatal(p)
	}
	path := base + "/" + p.ID
	for _, suffix := range []string{"", "/runs"} {
		w := request(t, h, "GET", path+suffix, bob, nil)
		if w.Code != 404 {
			t.Fatalf("cross workspace %s: %d", suffix, w.Code)
		}
	}
	if w := request(t, h, "POST", path+"/run", bob, map[string]any{"event_id": "other"}); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(t, h, "GET", base, "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	var run study.CollectionPlanRun
	data(t, request(t, h, "POST", path+"/run", alice, map[string]any{"event_id": "run-1"}), &run)
	if run.TaskID == "" || run.DispatchStatus != "queued" {
		t.Fatal(run)
	}
	var again study.CollectionPlanRun
	data(t, request(t, h, "POST", path+"/run", alice, map[string]any{"event_id": "run-1"}), &again)
	if again.ID != run.ID {
		t.Fatal(again)
	}
	if w := request(t, h, "POST", path+"/state", alice, map[string]any{"revision": p.Revision + 1, "enabled": false}); w.Code != 409 {
		t.Fatal(w.Code)
	}
	data(t, request(t, h, "POST", path+"/state", alice, map[string]any{"revision": p.Revision, "enabled": false}), &p)
	if p.Enabled {
		t.Fatal(p)
	}
	if w := request(t, h, "POST", path+"/run", alice, map[string]any{"event_id": "paused"}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	var history struct {
		Runs  []study.CollectionPlanRun `json:"runs"`
		Total int                       `json:"total"`
	}
	data(t, request(t, h, "GET", path+"/runs", alice, nil), &history)
	if history.Total != 1 || history.Runs[0].TaskStatus != "pending" {
		t.Fatal(history)
	}
	data(t, request(t, h, "POST", path+"/delete", alice, map[string]any{"revision": p.Revision}), &p)
	if p.DeletedAt == nil {
		t.Fatal(p)
	}
	data(t, request(t, h, "GET", path+"/runs", alice, nil), &history)
	if history.Total != 1 {
		t.Fatal(history)
	}
}

func TestCollectionWebhookScopeAndStrictPayload(t *testing.T) {
	_, h := collectionApp(t)
	_, token := managedLearner(t, h, "collection-hooks")
	base := "/api/v1/workspace/agent/collection-plans"
	body := collectionBody("webhook")
	body["allow_url"] = true
	var p study.CollectionPlan
	data(t, request(t, h, "POST", base, token, body), &p)
	path := base + "/" + p.ID
	var key struct {
		Plan      study.CollectionPlan `json:"plan"`
		AccessKey string               `json:"access_key"`
	}
	data(t, request(t, h, "POST", path+"/trigger-key", token, map[string]any{"revision": p.Revision}), &key)
	p = key.Plan
	hook := "/api/v1/collection-triggers/" + p.ID
	input := map[string]any{"event_id": "e-1", "sent_at": time.Now().UTC().Format(time.RFC3339), "url": "https://go.dev/blog/"}
	if w := request(t, h, "POST", hook, token, input); w.Code != 401 {
		t.Fatal("user token must not act as hook key", w.Code)
	}
	if w := request(t, h, "GET", base, key.AccessKey, nil); w.Code != 401 {
		t.Fatal("hook key must not read", w.Code)
	}
	if w := request(t, h, "POST", "/api/v1/agent/tasks/claim", key.AccessKey, map[string]any{}); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request(t, h, "POST", hook, key.AccessKey, input)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "task_config") || strings.Contains(w.Body.String(), "source_id") {
		t.Fatal("trigger response leaked config", w.Body.String())
	}
	var original map[string]any
	data(t, w, &original)
	w = request(t, h, "POST", hook, key.AccessKey, input)
	var retry map[string]any
	data(t, w, &retry)
	if retry["run_id"] != original["run_id"] {
		t.Fatal(retry)
	}
	input["event_id"] = "e-2"
	w = request(t, h, "POST", hook, key.AccessKey, input)
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" {
		t.Fatal(w.Code, w.Body.String())
	}
	input["owner_id"] = "attacker"
	w = request(t, h, "POST", hook, key.AccessKey, input)
	if w.Code != 400 {
		t.Fatal("unknown privilege parameter accepted", w.Code)
	}
	delete(input, "owner_id")
	data(t, request(t, h, "POST", path+"/trigger-key", token, map[string]any{"revision": p.Revision, "revoke": true}), &key)
	if w = request(t, h, "POST", hook, key.AccessKey, input); w.Code != 401 {
		t.Fatal("revoked key accepted", w.Code)
	}
}

func TestAdminCollectionPreviewAndValidation(t *testing.T) {
	_, h := collectionApp(t)
	base := "/api/v1/admin/agent/collection-plans"
	var out struct {
		Times []time.Time `json:"times"`
	}
	data(t, request(t, h, "POST", base+"/preview", adminKey, map[string]any{"schedule": map[string]any{"kind": "daily", "timezone": "Asia/Shanghai", "time_of_day": "09:00"}}), &out)
	if len(out.Times) != 5 {
		t.Fatal(out)
	}
	var p study.CollectionPlan
	data(t, request(t, h, "POST", base, adminKey, collectionBody("manual")), &p)
	body := collectionBody("manual")
	body["revision"] = p.Revision
	body["name"] = "已修改"
	data(t, request(t, h, "PUT", base+"/"+p.ID, adminKey, body), &p)
	if p.Name != "已修改" || p.Revision != 2 {
		t.Fatal(p)
	}
	w := request(t, h, "GET", base, adminKey, nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var raw map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &raw); e != nil {
		t.Fatal(e)
	}
	if w = request(t, h, "POST", base, adminKey, map[string]any{"name": "invalid", "owner_id": "x"}); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestCollectionDisabledRejectsDispatch(t *testing.T) {
	s, h := collectionApp(t)
	_, token := managedLearner(t, h, "collection-disabled")
	var p study.CollectionPlan
	data(t, request(t, h, "POST", "/api/v1/workspace/agent/collection-plans", token, collectionBody("manual")), &p)
	h = New(config.Config{Env: "test", AdminKey: adminKey, ReadKey: legacyReadKey, DataDir: t.TempDir(), AgentEnabled: false}, s, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if w := request(t, h, "POST", "/api/v1/workspace/agent/collection-plans/"+p.ID+"/run", token, map[string]any{"event_id": "disabled"}); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestCollectionWebhookUnauthenticatedRateLimit(t *testing.T) {
	_, h := collectionApp(t)
	for i := 0; i < 61; i++ {
		w := request(t, h, "POST", "/api/v1/collection-triggers/00000000-0000-0000-0000-000000000000", "wztg_invalid", map[string]any{"event_id": "invalid", "sent_at": time.Now().UTC()})
		if i < 60 && w.Code != 401 {
			t.Fatal(i, w.Code, w.Body.String())
		}
		if i == 60 && (w.Code != 429 || w.Header().Get("Retry-After") != "60") {
			t.Fatal(i, w.Code, w.Body.String())
		}
	}
}
func TestCollectionTriggerLimiterBoundedAndWindowReset(t *testing.T) {
	var limiter collectionTriggerLimiter
	now := time.Now()
	for i := 0; i < 60; i++ {
		if !limiter.allow("127.0.0.1:1234", now) {
			t.Fatal(i)
		}
	}
	if limiter.allow("127.0.0.1:5555", now) {
		t.Fatal("source port must not bypass per-IP limit")
	}
	if !limiter.allow("127.0.0.1:5555", now.Add(time.Minute)) {
		t.Fatal("window did not reset")
	}
}
