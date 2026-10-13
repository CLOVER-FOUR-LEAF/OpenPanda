// SPDX-License-Identifier: AGPL-3.0-or-later

package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xustalis/OpenPanda/internal/config"
	"github.com/Xustalis/OpenPanda/internal/memory"
)

// newMemoryPanel builds a panel handler with a configured memory root and
// projects store, so the memory endpoints exercise the real config-driven
// caps. Limits are shrunk so cap enforcement is testable with tiny payloads.
func newMemoryPanel(t *testing.T) (http.Handler, *config.Config) {
	t.Helper()
	cfg := config.Default()
	cfg.Storage.MemoryPath = t.TempDir()
	cfg.Storage.ProjectsPath = t.TempDir()
	cfg.Memory.Limits.User = 40
	cfg.Memory.Limits.Memory = 60
	cfg.Memory.Limits.Project = 80
	limits := memory.Limits{
		User:    cfg.Memory.Limits.User,
		Memory:  cfg.Memory.Limits.Memory,
		Project: cfg.Memory.Limits.Project,
	}
	h := New(Deps{
		Store:     newTestStore(t),
		Projects:  memory.NewProjectsWithLimits(cfg.Storage.ProjectsPath, limits),
		Cfg:       cfg,
		StaticDir: t.TempDir(),
		Token:     testToken,
	})
	return h, cfg
}

func doJSON(t *testing.T, h http.Handler, req *http.Request) (int, map[string]any) {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var out map[string]any
	if rr.Code/100 == 2 {
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal %s: %v", rr.Body.String(), err)
		}
	}
	return rr.Code, out
}

func TestGetMemoryExposesTopicsAndConfiguredLimits(t *testing.T) {
	h, cfg := newMemoryPanel(t)
	hermes := memory.NewHermesWithLimits(cfg.Storage.MemoryPath, memory.Limits{User: 40, Memory: 60})
	if err := hermes.SaveUser(memory.MemFile{Entries: []string{"user likes tea"}}); err != nil {
		t.Fatal(err)
	}
	if err := hermes.SaveTopic("work", memory.MemFile{Entries: []string{"team ships fridays"}}); err != nil {
		t.Fatal(err)
	}

	code, out := doJSON(t, h, authedReq(http.MethodGet, "/api/memory", nil))
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if out["user_limit"] != float64(40) || out["mem_limit"] != float64(60) || out["project_limit"] != float64(80) {
		t.Errorf("limits must reflect config: %+v", out)
	}
	topics, ok := out["topics"].([]any)
	if !ok || len(topics) != 1 {
		t.Fatalf("topics = %v, want the one work topic", out["topics"])
	}
	topic := topics[0].(map[string]any)
	if topic["name"] != "work" || !strings.Contains(topic["content"].(string), "team ships fridays") {
		t.Errorf("topic payload wrong: %+v", topic)
	}
}

func TestPutMemoryEnforcesConfiguredLimit(t *testing.T) {
	h, cfg := newMemoryPanel(t)

	// Over the configured 40-char user cap: rejected with the store error.
	body := `{"content":"` + strings.Repeat("x", 50) + `"}`
	req := authedReq(http.MethodPut, "/api/memory/user", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("over-limit status = %d, want 400", rr.Code)
	}

	// Within the cap: accepted, and the file lands on disk.
	code, _ := doJSON(t, h, authedReq(http.MethodPut, "/api/memory/user", strings.NewReader(`{"content":"user likes tea"}`)))
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	data, err := os.ReadFile(filepath.Join(cfg.Storage.MemoryPath, "USER.md"))
	if err != nil || !strings.Contains(string(data), "user likes tea") {
		t.Errorf("USER.md not written: %q err %v", data, err)
	}
}

func TestPutMemoryTopicCRUDAndTraversal(t *testing.T) {
	h, cfg := newMemoryPanel(t)

	code, out := doJSON(t, h, authedReq(http.MethodPut, "/api/memory/topics/work", strings.NewReader(`{"content":"team ships fridays"}`)))
	if code != http.StatusOK {
		t.Fatalf("create topic status = %d", code)
	}
	if out["limit"] != float64(60) {
		t.Errorf("topic limit = %v, want configured 60", out["limit"])
	}
	if _, err := os.Stat(filepath.Join(cfg.Storage.MemoryPath, "topics", "work.md")); err != nil {
		t.Fatalf("topic file missing: %v", err)
	}

	// GET surfaces the new topic.
	_, got := doJSON(t, h, authedReq(http.MethodGet, "/api/memory", nil))
	if topics := got["topics"].([]any); len(topics) != 1 {
		t.Fatalf("topics = %v", got["topics"])
	}

	// DELETE removes it; deleting again is a 404.
	if code, _ := doJSON(t, h, authedReq(http.MethodDelete, "/api/memory/topics/work", nil)); code != http.StatusOK {
		t.Fatalf("delete status = %d", code)
	}
	if code, _ := doJSON(t, h, authedReq(http.MethodDelete, "/api/memory/topics/work", nil)); code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", code)
	}

	// Directory traversal: encoded separators must never write outside
	// topics/, regardless of the status the mux answers with.
	for _, evil := range []string{
		"/api/memory/topics/..%2Fevil",
		"/api/memory/topics/..%2F..%2Fevil",
		"/api/memory/topics/%2e%2e",
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, authedReq(http.MethodPut, evil, strings.NewReader(`{"content":"pwned"}`)))
	}
	for _, dir := range []string{cfg.Storage.MemoryPath, filepath.Dir(cfg.Storage.MemoryPath)} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.Contains(e.Name(), "evil") || e.Name() == ".." {
				t.Fatalf("traversal escaped topics/: %s/%s", dir, e.Name())
			}
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.Storage.MemoryPath, "..", "evil.md")); !os.IsNotExist(err) {
		t.Fatalf("evil.md written outside topics/: %v", err)
	}

	// Over the memory cap the topic write is rejected like any other file.
	big := `{"content":"` + strings.Repeat("b", 100) + `"}`
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, authedReq(http.MethodPut, "/api/memory/topics/big", strings.NewReader(big)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("over-limit topic status = %d, want 400", rr.Code)
	}
}

func TestDeleteMemoryDaily(t *testing.T) {
	h, cfg := newMemoryPanel(t)

	dailyDir := filepath.Join(cfg.Storage.MemoryPath, "daily")
	if err := os.MkdirAll(dailyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dailyDir, "2026-10-01.md"), []byte("- 10:00:00 log"), 0o644); err != nil {
		t.Fatal(err)
	}

	// DELETE removes it; deleting again is a 404.
	if code, _ := doJSON(t, h, authedReq(http.MethodDelete, "/api/memory/daily/2026-10-01", nil)); code != http.StatusOK {
		t.Fatalf("delete status = %d", code)
	}
	if code, _ := doJSON(t, h, authedReq(http.MethodDelete, "/api/memory/daily/2026-10-01", nil)); code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", code)
	}

	// Traversal names must never resolve outside daily/.
	for _, evil := range []string{
		"/api/memory/daily/..%2Fevil",
		"/api/memory/daily/..%2F..%2Fevil",
		"/api/memory/daily/%2e%2e",
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, authedReq(http.MethodDelete, evil, nil))
		if rr.Code == http.StatusOK {
			t.Fatalf("traversal delete %s accepted", evil)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.Storage.MemoryPath, "..", "evil.md")); !os.IsNotExist(err) {
		t.Fatalf("evil.md deleted outside daily/: %v", err)
	}

	// The archive directory is not a daily file — deleting "archive" 404s.
	if err := os.MkdirAll(filepath.Join(dailyDir, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _ := doJSON(t, h, authedReq(http.MethodDelete, "/api/memory/daily/archive", nil)); code != http.StatusNotFound {
		t.Fatalf("archive delete status = %d, want 404", code)
	}
}

func TestPruneMemoryDaily(t *testing.T) {
	h, cfg := newMemoryPanel(t)

	dailyDir := filepath.Join(cfg.Storage.MemoryPath, "daily")
	if err := os.MkdirAll(dailyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -100).Format("2006-01-02") + ".md"
	recent := time.Now().Format("2006-01-02") + ".md"
	for _, name := range []string{old, recent} {
		if err := os.WriteFile(filepath.Join(dailyDir, name), []byte("log"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	code, out := doJSON(t, h, authedReq(http.MethodPost, "/api/memory/daily/prune", nil))
	if code != http.StatusOK || out["status"] != "pruned" {
		t.Fatalf("prune status = %d out = %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(dailyDir, old)); !os.IsNotExist(err) {
		t.Errorf("expired daily should be deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dailyDir, recent)); err != nil {
		t.Errorf("recent daily should remain: %v", err)
	}
}

func TestPutProjectMemory(t *testing.T) {
	h, cfg := newMemoryPanel(t)

	code, out := doJSON(t, h, authedReq(http.MethodPut, "/api/projects/demo/memory", strings.NewReader(`{"content":"demo uses go"}`)))
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if out["limit"] != float64(80) {
		t.Errorf("limit = %v, want configured 80", out["limit"])
	}
	data, err := os.ReadFile(filepath.Join(cfg.Storage.ProjectsPath, "demo", "MEMORY.md"))
	if err != nil || !strings.Contains(string(data), "demo uses go") {
		t.Fatalf("project memory not written: %q err %v", data, err)
	}

	// Traversal names are rejected.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, authedReq(http.MethodPut, "/api/projects/..%2Fevil/memory", strings.NewReader(`{"content":"x"}`)))
	if rr.Code == http.StatusOK {
		t.Fatalf("traversal project name accepted")
	}

	// The configured project cap applies.
	big := `{"content":"` + strings.Repeat("p", 100) + `"}`
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, authedReq(http.MethodPut, "/api/projects/demo/memory", strings.NewReader(big)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("over-limit project memory status = %d, want 400", rr.Code)
	}
}
