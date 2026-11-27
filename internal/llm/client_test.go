package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Twinkle661/NamiDiary/internal/testutil"
)

func TestOverridesAndJSONMode(t *testing.T) {
	c := testutil.Config(t).Memory.LLM
	temp := 0.7
	c.Agents.Dream.Model = "override-model"
	c.Agents.Dream.Temperature = &temp
	s := testutil.Provider(t, func(req map[string]any) string {
		if req["model"] != "override-model" || req["temperature"] != 0.7 || req["response_format"] == nil {
			t.Errorf("wrong LLM options %+v", req)
		}
		return `{"actions":[]}`
	})
	c.BaseURL = s.URL
	out, err := New(c).Complete(context.Background(), "dream", []Message{{Role: "user", Content: "data"}})
	if err != nil || out != `{"actions":[]}` {
		t.Fatalf("%s %v", out, err)
	}
}

func TestProviderErrorKeepsBody(t *testing.T) {
	c := testutil.Config(t).Memory.LLM
	body := `{"error":{"message":"max_tokens exceeds model limit","type":"invalid_request_error"}}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, body, http.StatusBadRequest)
	}))
	t.Cleanup(s.Close)
	c.BaseURL = s.URL
	_, err := New(c).Complete(context.Background(), "dream", []Message{{Role: "user", Content: "data"}})
	if err == nil || !strings.Contains(err.Error(), "LLM_REQUEST_ERROR") || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), body) {
		t.Fatalf("error lacks provider detail: %v", err)
	}
	if strings.Contains(err.Error(), c.APIKey) {
		t.Fatal("API key leaked into error")
	}
}
