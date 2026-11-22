package rest_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	apimcp "github.com/Twinkle661/NamiDiary/internal/api/mcp"
	"github.com/Twinkle661/NamiDiary/internal/api/rest"
	"github.com/Twinkle661/NamiDiary/internal/diary"
	"github.com/Twinkle661/NamiDiary/internal/embedding"
	"github.com/Twinkle661/NamiDiary/internal/httpx"
	"github.com/Twinkle661/NamiDiary/internal/llm"
	"github.com/Twinkle661/NamiDiary/internal/memory"
	"github.com/Twinkle661/NamiDiary/internal/memory/agents"
	"github.com/Twinkle661/NamiDiary/internal/memory/backup"
	"github.com/Twinkle661/NamiDiary/internal/memory/worker"
	"github.com/Twinkle661/NamiDiary/internal/testutil"
)

func TestFullServer(t *testing.T) {
	c := testutil.Config(t)
	provider := testutil.Provider(t, func(map[string]any) string {
		return `{"actions":[{"op":"add","topic":"profile","content":"A durable memory"}]}`
	})
	c.Diary.Embedding.BaseURL = provider.URL
	c.Memory.LLM.BaseURL = provider.URL
	d, err := diary.Open(c, embedding.New(c.Diary.Embedding))
	if err != nil {
		t.Fatal(err)
	}
	defer d.DB.Close()
	m, err := memory.Open(filepath.Join(c.App.DataDir, "memory.db"), c.Memory)
	if err != nil {
		t.Fatal(err)
	}
	defer m.DB.Close()
	b := backup.New(m, c.App.DataDir, c.Memory.Backup.Retention)
	r := &agents.Runner{Store: m, Diary: d, Client: llm.New(c.Memory.LLM), Config: c.Memory, Location: time.UTC}
	w := worker.New(context.Background(), m, r, b)
	defer w.Stop()
	d.OnAdded = w.EnqueueDream
	a := &rest.API{Config: c, Diary: d, Memory: m, Worker: w, Backup: b}
	mux := http.NewServeMux()
	a.Register(mux)
	server, mcpHandler := apimcp.New(a, "test")
	mux.Handle("/mcp", mcpHandler)
	ts := httptest.NewServer(httpx.Middleware(c, mux))
	defer ts.Close()
	request := func(method, path, body string, auth bool) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		if auth {
			req.Header.Set("Authorization", "Bearer "+c.App.APIKey)
		}
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(data)
	}
	if status, _ := request("GET", "/healthz", "", false); status != 200 {
		t.Fatal("health auth exemption")
	}
	if status, _ := request("GET", "/memory/topics", "", false); status != 401 {
		t.Fatal("missing auth accepted")
	}
	for _, date := range []string{"2026-01-01", "2026-01-03", "2026-01-05"} {
		status, body := request("POST", "/diary/"+date, `{"content":"A diary entry","meta":{"summary":"reading","location":"City"}}`, true)
		if status != 201 {
			t.Fatalf("add %d %s", status, body)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := w.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !status.Busy && !status.PendingDream && len(status.LastJobs) > 0 {
			for _, job := range status.LastJobs {
				if !job.OK {
					t.Fatal(job.Error)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for path, want := range map[string]string{"/memory/entries": "A durable memory", "/context/static": "core_memory", "/diary/latest?recent=3": "location: City"} {
		status, body := request("GET", path, "", true)
		if status != 200 || !strings.Contains(body, want) {
			t.Fatalf("%s: %d %s", path, status, body)
		}
	}
	status, body := request("POST", "/context/dynamic", `{"user_message":"reading"}`, true)
	if status != 200 || !strings.Contains(body, "related_diaries") {
		t.Fatalf("dynamic %d %s", status, body)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"POST", "/diary/normalize", "", 200}, {"POST", "/diary/init", "", 200}, {"POST", "/diary/patch", `{"content":"new"}`, 200}, {"POST", "/diary/similar", `{}`, 400}, {"GET", "/diary/latest?recent=0", "", 400}, {"GET", "/context/static?recent=-1", "", 400}, {"POST", "/memory/entries", `{"topic":"profile","content":"bad","updated_by":"dream"}`, 400}, {"POST", "/diary/patch", `{"meta":{"location":"forbidden"}}`, 400}, {"GET", "/prompt", "", 404}} {
		status, body := request(tc.method, tc.path, tc.body, true)
		if status != tc.status {
			t.Errorf("%s: %d %s", tc.path, status, body)
		}
	}
	// Inspect the actual SDK registry and call a tool over an in-memory MCP transport.
	st, ct := sdk.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 15 {
		t.Fatalf("tools %d", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if strings.Contains(tool.Name, "init") || strings.Contains(tool.Name, "restore") || strings.Contains(tool.Name, "snapshots") {
			t.Fatal("destructive maintenance exposed")
		}
	}
	out, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "memory_add", Arguments: map[string]any{"topic": "preference", "content": "An intentional fact"}})
	if err != nil || out.IsError {
		t.Fatalf("MCP write %v %+v", err, out)
	}
	lib, _ := m.Library(context.Background())
	if lib[lib.Resolve("preference")].Entries[0].UpdatedBy != memory.Agent {
		t.Fatal("MCP provenance")
	}
	status, body = request("GET", "/memory/status", "", true)
	var result map[string]any
	if status != 200 || json.Unmarshal([]byte(body), &result) != nil {
		t.Fatal("status response")
	}
}
