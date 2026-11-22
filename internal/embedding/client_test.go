package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Twinkle661/NamiDiary/internal/config"
)

func TestBatchesOrderingAndConcurrency(t *testing.T) {
	var active, maxActive atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maxActive.Load()
			if n <= old || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(time.Millisecond * 5)
		var in struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		data := []any{}
		for i := len(in.Input) - 1; i >= 0; i-- {
			data = append(data, map[string]any{"index": i, "embedding": []float32{float32(len(in.Input[i])), 1}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer s.Close()
	c := config.Defaults().Diary.Embedding
	c.BaseURL = s.URL
	c.BatchSize = 2
	c.Concurrency = 2
	out, err := New(c).Embed(context.Background(), []string{"a", "bb", "ccc", "dddd", "eeeee", "ffffff"})
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range out {
		if int(v[0]) != i+1 {
			t.Fatal("ordering")
		}
	}
	if maxActive.Load() > 2 {
		t.Fatal("concurrency exceeded")
	}
}
func TestProviderFailures(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":[{"index":0,"embedding":[]}]}`, `{"data":[{"index":2,"embedding":[1]}]}`, `{"data":[{"index":0,"embedding":[1e100]}]}`, `not-json`} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer s.Close()
			c := config.Defaults().Diary.Embedding
			c.BaseURL = s.URL
			if _, err := New(c).Embed(context.Background(), []string{"text"}); err == nil {
				t.Fatal("bad reply accepted")
			}
		})
	}
}
