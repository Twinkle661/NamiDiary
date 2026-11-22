// Package testutil provides isolated local fixtures; providers never use the network.
package testutil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Twinkle661/NamiDiary/internal/config"
	"github.com/Twinkle661/NamiDiary/internal/memory"
)

func Config(t testing.TB) config.Config {
	t.Helper()
	c := config.Defaults()
	c.App.DataDir = t.TempDir()
	c.App.APIKey = "test-secret"
	c.Diary.Embedding.APIKey = "test-embedding-secret"
	c.Memory.LLM.APIKey = "test-llm-secret"
	c.Diary.Embedding.Dimensions = 3
	return c
}
func Store(t testing.TB) (*memory.Store, config.Config) {
	t.Helper()
	c := Config(t)
	s, err := memory.Open(filepath.Join(c.App.DataDir, "memory.db"), c.Memory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s, c
}

type Embeddings struct {
	Mu     sync.Mutex
	Inputs []string
	Err    error
}

func (e *Embeddings) Embed(ctx context.Context, in []string) ([][]float32, error) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if e.Err != nil {
		return nil, e.Err
	}
	e.Inputs = append(e.Inputs, in...)
	out := make([][]float32, len(in))
	for i := range in {
		out[i] = []float32{float32(len(in[i])), 1, 0}
	}
	return out, nil
}
func Provider(t testing.TB, reply func(map[string]any) string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/embeddings" {
			inputs := req["input"].([]any)
			data := []any{}
			for i, input := range inputs {
				data = append(data, map[string]any{"index": i, "embedding": []float32{float32(len(input.(string))), 1, 0}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		text := `{"actions":[]}`
		if reply != nil {
			text = reply(req)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": text}}}})
	}))
	t.Cleanup(s.Close)
	return s
}
