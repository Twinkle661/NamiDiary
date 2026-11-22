package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/config"
)

type Client struct {
	Config config.Embedding
	HTTP   *http.Client
}

func New(c config.Embedding) *Client { return &Client{c, &http.Client{Timeout: c.Timeout}} }
func (c *Client) Embed(ctx context.Context, input []string) ([][]float32, error) {
	if len(input) == 0 {
		return [][]float32{}, nil
	}
	if c.Config.BatchSize < 1 || c.Config.Concurrency < 1 {
		return nil, apperr.New(500, "EMBEDDING_CONFIG_ERROR", "Invalid embedding configuration.")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := make([][]float32, len(input))
	sem := make(chan struct{}, c.Config.Concurrency)
	var wg sync.WaitGroup
	var first error
	var mu sync.Mutex
	for start := 0; start < len(input); start += c.Config.BatchSize {
		end := min(start+c.Config.BatchSize, len(input))
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			v, err := c.batch(ctx, input[start:end])
			if err != nil {
				mu.Lock()
				if first == nil {
					first = err
					cancel()
				}
				mu.Unlock()
				return
			}
			copy(out[start:end], v)
		}(start, end)
	}
	wg.Wait()
	if first != nil {
		return nil, first
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	dim := len(out[0])
	for _, v := range out {
		if len(v) != dim {
			return nil, apperr.New(502, "EMBEDDING_RESPONSE_ERROR", "Inconsistent embedding dimensions.")
		}
	}
	return out, nil
}
func (c *Client) batch(ctx context.Context, input []string) ([][]float32, error) {
	b, _ := json.Marshal(map[string]any{"model": c.Config.Model, "input": input})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.Config.BaseURL, "/")+"/embeddings", bytes.NewReader(b))
	if err != nil {
		return nil, apperr.New(500, "EMBEDDING_CONFIG_ERROR", "Invalid embedding URL.")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Config.APIKey)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, &apperr.Error{Status: 502, Code: "EMBEDDING_REQUEST_ERROR", Message: "Embedding request failed.", Cause: err}
	}
	defer res.Body.Close()
	b, err = io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		slog.Error("Embedding provider failed", "status", res.StatusCode, "body", string([]rune(string(b))[:min(1000, len([]rune(string(b))))]))
		return nil, apperr.New(502, "EMBEDDING_PROVIDER_ERROR", "Embedding provider rejected the request.")
	}
	var data struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err = json.Unmarshal(b, &data); err != nil || len(data.Data) != len(input) {
		return nil, apperr.New(502, "EMBEDDING_RESPONSE_ERROR", "Invalid embedding response count or JSON.")
	}
	out := make([][]float32, len(input))
	for _, v := range data.Data {
		if v.Index < 0 || v.Index >= len(out) || out[v.Index] != nil || len(v.Embedding) == 0 {
			return nil, apperr.New(502, "EMBEDDING_RESPONSE_ERROR", "Invalid embedding index or dimensions.")
		}
		for _, x := range v.Embedding {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return nil, apperr.New(502, "EMBEDDING_RESPONSE_ERROR", "Embedding contains non-finite values.")
			}
		}
		out[v.Index] = v.Embedding
	}
	slog.Debug("Embedding batch completed", "count", len(input), "dimensions", fmt.Sprint(len(out[0])))
	return out, nil
}
