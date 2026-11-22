package llm

import (
	"context"
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
