package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	apimcp "github.com/Twinkle661/NamiDiary/internal/api/mcp"
	"github.com/Twinkle661/NamiDiary/internal/api/rest"
	"github.com/Twinkle661/NamiDiary/internal/config"
	"github.com/Twinkle661/NamiDiary/internal/diary"
	"github.com/Twinkle661/NamiDiary/internal/embedding"
	"github.com/Twinkle661/NamiDiary/internal/httpx"
	"github.com/Twinkle661/NamiDiary/internal/llm"
	"github.com/Twinkle661/NamiDiary/internal/memory"
	"github.com/Twinkle661/NamiDiary/internal/memory/agents"
	"github.com/Twinkle661/NamiDiary/internal/memory/backup"
	"github.com/Twinkle661/NamiDiary/internal/memory/worker"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		port := os.Getenv("NAMIDIARY_APP__PORT")
		if port == "" {
			port = "8188"
		}
		client := http.Client{Timeout: 3 * time.Second}
		res, err := client.Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil {
			os.Exit(1)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("Service stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	if c.App.Debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	slog.Debug("Effective configuration", "config", c.Masked())
	if err = os.MkdirAll(c.App.DataDir, 0700); err != nil {
		return err
	}
	d, err := diary.Open(c, embedding.New(c.Diary.Embedding))
	if err != nil {
		return err
	}
	defer d.DB.Close()
	m, b, err := openMemory(context.Background(), c)
	if err != nil {
		return err
	}
	defer m.DB.Close()
	loc, _ := time.LoadLocation(c.App.Timezone)
	runner := &agents.Runner{Store: m, Diary: d, Client: llm.New(c.Memory.LLM), Config: c.Memory, Location: loc}
	w := worker.New(context.Background(), m, runner, b)
	defer w.Stop()
	d.OnAdded = w.EnqueueDream
	api := &rest.API{Config: c, Diary: d, Memory: m, Worker: w, Backup: b}
	mux := http.NewServeMux()
	api.Register(mux)
	_, mcpHandler := apimcp.New(api, version)
	mux.Handle("/mcp", mcpHandler)
	server := &http.Server{Addr: fmt.Sprintf(":%d", c.App.Port), Handler: httpx.Middleware(c, mux), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, 1)
	go func() {
		slog.Info("Server listening", "port", c.App.Port, "version", version)
		errs <- server.ListenAndServe()
	}()
	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		w.Stop()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func openMemory(ctx context.Context, c config.Config) (*memory.Store, *backup.Manager, error) {
	path := filepath.Join(c.App.DataDir, "memory.db")
	_, statErr := os.Stat(path)
	missing := os.IsNotExist(statErr)
	if statErr != nil && !missing {
		return nil, nil, statErr
	}
	snapshot := ""
	if missing {
		probe := backup.New(nil, c.App.DataDir, c.Memory.Backup.Retention)
		ss, err := probe.List()
		if err != nil {
			return nil, nil, err
		}
		if len(ss) > 0 {
			snapshot = ss[0].Name
			if _, err = probe.Read(snapshot); err != nil {
				return nil, nil, err
			}
		}
	}
	m, err := memory.Open(path, c.Memory)
	if err != nil {
		return nil, nil, err
	}
	b := backup.New(m, c.App.DataDir, c.Memory.Backup.Retention)
	if snapshot != "" {
		slog.Warn("Restoring missing memory database from newest snapshot", "snapshot", snapshot)
		if err = b.Restore(ctx, snapshot, false); err != nil {
			m.DB.Close()
			_ = os.Remove(path)
			_ = os.Remove(path + "-wal")
			_ = os.Remove(path + "-shm")
			return nil, nil, err
		}
	}
	return m, b, nil
}
