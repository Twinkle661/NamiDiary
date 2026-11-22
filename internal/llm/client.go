package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/config"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type Client struct {
	Config config.LLM
	HTTP   *http.Client
}

func New(c config.LLM) *Client { return &Client{c, &http.Client{Timeout: c.Timeout}} }
func (c *Client) Complete(ctx context.Context, agent string, messages []Message) (string, error) {
	model, temp := c.Config.Model, c.Config.Temperature
	var o config.Override
	switch agent {
	case "dream":
		o = c.Config.Agents.Dream
	case "archive":
		o = c.Config.Agents.Archive
	case "compact":
		o = c.Config.Agents.Compact
	}
	if o.Model != "" {
		model = o.Model
	}
	if o.Temperature != nil {
		temp = *o.Temperature
	}
	body := map[string]any{"model": model, "temperature": temp, "max_tokens": c.Config.MaxTokens, "messages": messages}
	if c.Config.JSONMode {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	b, _ := json.Marshal(body)
	slog.Debug("LLM request", "agent", agent, "messages", messages)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.Config.BaseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Config.APIKey)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", &apperr.Error{Status: 502, Code: "LLM_REQUEST_ERROR", Message: "LLM request failed.", Cause: err}
	}
	defer res.Body.Close()
	b, err = io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		slog.Error("LLM provider failed", "status", res.StatusCode)
		return "", apperr.New(502, "LLM_REQUEST_ERROR", "LLM provider rejected the request.")
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Usage any `json:"usage"`
	}
	if err = json.Unmarshal(b, &out); err != nil || len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return "", apperr.New(502, "LLM_RESPONSE_ERROR", "Missing LLM reply.")
	}
	slog.Info("LLM response", "agent", agent, "usage", out.Usage)
	slog.Debug("LLM reply", "reply", out.Choices[0].Message.Content)
	return out.Choices[0].Message.Content, nil
}
