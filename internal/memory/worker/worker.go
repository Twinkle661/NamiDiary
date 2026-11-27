package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/diary"
	"github.com/Twinkle661/NamiDiary/internal/memory"
	"github.com/Twinkle661/NamiDiary/internal/memory/agents"
	"github.com/Twinkle661/NamiDiary/internal/memory/backup"
)

type Job struct {
	Type     string `json:"type"`
	Date     string `json:"date,omitempty"`
	Started  string `json:"started"`
	Finished string `json:"finished,omitempty"`
	Step     string `json:"step,omitempty"`
	Attempt  int    `json:"attempt,omitempty"`
	OK       bool   `json:"ok"`
	Summary  string `json:"summary,omitempty"`
	Error    string `json:"error,omitempty"`
	Recent   int    `json:"-"`
	Snapshot string `json:"-"`
}
type Status struct {
	Busy            bool               `json:"busy"`
	CurrentJob      *Job               `json:"current_job"`
	PendingDream    bool               `json:"pending_dream"`
	LastJobs        []Job              `json:"last_jobs"`
	Violations      []memory.Violation `json:"violations"`
	PendingArchives []string           `json:"pending_archives"`
	LastSnapshot    string             `json:"last_snapshot"`
}
type Worker struct {
	Store              *memory.Store
	Runner             *agents.Runner
	Backup             *backup.Manager
	ctx                context.Context
	cancel             context.CancelFunc
	done               chan struct{}
	wake               chan struct{}
	jobs               chan Job
	gate               sync.Mutex
	mu                 sync.Mutex
	current            *Job
	last               []Job
	pendingDream       bool
	pendingMaintenance bool
	stopped            bool
}

func New(parent context.Context, s *memory.Store, r *agents.Runner, b *backup.Manager) *Worker {
	ctx, cancel := context.WithCancel(parent)
	w := &Worker{Store: s, Runner: r, Backup: b, ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1), jobs: make(chan Job, 1), last: []Job{}}
	r.AfterChange = func(ctx context.Context) error { _, err := b.Snapshot(ctx); return err }
	r.Progress = w.progress
	go w.loop()
	return w
}
func (w *Worker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *Worker) EnqueueDream(_ string) {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.pendingDream = true
	w.mu.Unlock()
	w.signal()
}
func (w *Worker) Request(job string, recent int, snapshot string) error {
	if job != "maintenance" && job != "init" && job != "restore" {
		return apperr.Invalid("Unknown job.")
	}
	if recent < 0 || recent > 365 {
		return apperr.Invalid("Recent must be in 0..365.")
	}
	if !w.gate.TryLock() {
		return apperr.Busy("MEMORY")
	}
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		w.gate.Unlock()
		return apperr.Busy("MEMORY")
	}
	j := Job{Type: job, Recent: recent, Snapshot: snapshot, Started: time.Now().UTC().Format(time.RFC3339Nano)}
	w.current = &j
	w.jobs <- j
	w.mu.Unlock()
	return nil
}

// RequestDream starts a dream job now. An empty date runs exactly like the
// automatic post-add dream (newest diaries, today's date); a date replays as
// of that day, like an init pass. Callers validate that the diary exists.
func (w *Worker) RequestDream(date string) error {
	if !w.gate.TryLock() {
		return apperr.Busy("MEMORY")
	}
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		w.gate.Unlock()
		return apperr.Busy("MEMORY")
	}
	j := Job{Type: "dream", Date: date, Started: time.Now().UTC().Format(time.RFC3339Nano)}
	w.current = &j
	w.jobs <- j
	w.mu.Unlock()
	return nil
}
func (w *Worker) Apply(ctx context.Context, src memory.Source, a []memory.Action) (memory.BatchResult, error) {
	if !w.gate.TryLock() {
		return memory.BatchResult{}, apperr.Busy("MEMORY")
	}
	defer func() { w.gate.Unlock(); w.signal() }()
	w.mu.Lock()
	stopped := w.stopped
	w.mu.Unlock()
	if stopped {
		return memory.BatchResult{}, apperr.Busy("MEMORY")
	}
	result, err := w.Store.Apply(ctx, src, a, memory.ApplyOptions{})
	if err != nil {
		return result, err
	}
	if result.Changed {
		if _, err = w.Backup.Snapshot(ctx); err != nil {
			return result, err
		}
		l, err := w.Store.Library(ctx)
		if err != nil {
			return result, err
		}
		need := len(memory.Violations(l, w.Store.Config.Limits)) > 0
		for _, t := range l {
			need = need || t.Archived != nil
		}
		if need {
			w.mu.Lock()
			w.pendingMaintenance = true
			w.mu.Unlock()
		}
	}
	return result, nil
}
func (w *Worker) progress(step string, attempt int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.current != nil {
		w.current.Step = step
		w.current.Attempt = attempt
	}
	slog.Info("Memory job step", "step", step, "attempt", attempt)
}
func (w *Worker) Status(ctx context.Context) (Status, error) {
	w.mu.Lock()
	s := Status{Busy: w.current != nil, PendingDream: w.pendingDream, LastJobs: append([]Job{}, w.last...), PendingArchives: []string{}}
	if w.current != nil {
		j := *w.current
		s.CurrentJob = &j
	}
	w.mu.Unlock()
	l, err := w.Store.Library(ctx)
	if err != nil {
		return s, err
	}
	s.Violations = memory.Violations(l, w.Store.Config.Limits)
	for _, t := range l {
		if t.Archived != nil {
			s.PendingArchives = append(s.PendingArchives, t.Topic)
		}
	}
	s.LastSnapshot = w.Store.Meta(ctx, "last_snapshot")
	return s, nil
}
func (w *Worker) Stop() {
	w.mu.Lock()
	w.stopped = true
	if w.pendingDream {
		slog.Warn("Dropping pending dream on shutdown")
	}
	w.pendingDream = false
	w.mu.Unlock()
	w.cancel()
	<-w.done
}
func (w *Worker) loop() {
	defer close(w.done)
	var ticks <-chan time.Time
	var timer *time.Ticker
	if w.Store.Config.Maintenance.Interval > 0 {
		timer = time.NewTicker(w.Store.Config.Maintenance.Interval)
		ticks = timer.C
		defer timer.Stop()
	}
	for {
		select {
		case <-w.ctx.Done():
			return
		case j := <-w.jobs:
			w.run(j)
		case <-ticks:
			_ = w.Request("maintenance", 0, "")
		case <-w.wake:
			if !w.gate.TryLock() {
				continue
			}
			w.mu.Lock()
			j := Job{Started: time.Now().UTC().Format(time.RFC3339Nano)}
			if w.pendingDream {
				j.Type = "dream"
				w.pendingDream = false
			} else if w.pendingMaintenance {
				j.Type = "maintenance"
				w.pendingMaintenance = false
			}
			if j.Type != "" {
				w.current = &j
			}
			w.mu.Unlock()
			if j.Type == "" {
				w.gate.Unlock()
				continue
			}
			w.run(j)
		}
	}
}
func (w *Worker) run(j Job) {
	slog.Info("Memory job started", "job", j.Type, "date", j.Date)
	var err error
	switch j.Type {
	case "dream":
		var until *string
		if j.Date != "" {
			until = &j.Date
		}
		err = w.Runner.Run(w.ctx, "dream", until, "")
		err = errors.Join(err, w.maintain(w.ctx))
	case "maintenance":
		err = w.maintain(w.ctx)
	case "restore":
		err = w.Backup.Restore(w.ctx, j.Snapshot, true)
	case "init":
		err = w.init(w.ctx, j.Recent)
	}
	j.Finished = time.Now().UTC().Format(time.RFC3339Nano)
	j.OK = err == nil
	if err != nil {
		j.Error = err.Error()
		slog.Error("Memory job failed", "job", j.Type, "error", err)
	} else {
		j.Summary = "Completed"
		slog.Info("Memory job finished", "job", j.Type)
	}
	w.mu.Lock()
	w.last = append([]Job{j}, w.last...)
	if len(w.last) > 20 {
		w.last = w.last[:20]
	}
	w.current = nil
	w.mu.Unlock()
	w.gate.Unlock()
	w.signal()
}
func (w *Worker) maintain(ctx context.Context) error {
	var errs []error
	w.progress("integrity", 0)
	changed, err := w.Store.Repair(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	if changed {
		if _, err = w.Backup.Snapshot(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	l, err := w.Store.Library(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].Archived == nil {
			return false
		}
		if l[j].Archived == nil {
			return true
		}
		return *l[i].Archived < *l[j].Archived
	})
	for _, t := range l {
		if t.Archived != nil {
			if err = w.Runner.Run(ctx, "archive", nil, t.Topic); err != nil {
				errs = append(errs, err)
			}
		}
	}
	l, err = w.Store.Library(ctx)
	if err != nil {
		errs = append(errs, err)
	} else if len(memory.Violations(l, w.Store.Config.Limits)) > 0 {
		if err = w.Runner.Run(ctx, "compact", nil, ""); err != nil {
			errs = append(errs, err)
		}
	}
	if w.Store.Meta(ctx, "last_change") != w.Store.Meta(ctx, "last_snapshot_change") {
		w.progress("snapshot", 0)
		if _, err = w.Backup.Snapshot(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	w.progress("prune", 0)
	if err = w.Backup.Prune(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
func (w *Worker) init(ctx context.Context, recent int) error {
	w.progress("snapshot", 0)
	if _, err := w.Backup.Snapshot(ctx); err != nil {
		return err
	}
	if err := w.Store.Replace(ctx, memory.Empty()); err != nil {
		return err
	}
	if _, err := w.Backup.Snapshot(ctx); err != nil {
		return err
	}
	if recent == 0 {
		return nil
	}
	es, err := w.Runner.Diary.Latest(ctx, diary.LatestQuery{N: recent})
	if err != nil {
		return err
	}
	for i, e := range es {
		w.progress(fmt.Sprintf("replay_%d_of_%d", i+1, len(es)), 0)
		if err = w.Runner.Run(ctx, "dream", &e.Date, ""); err != nil {
			return fmt.Errorf("init pass %d (%s): %w", i+1, e.Date, err)
		}
		if err = w.maintain(ctx); err != nil {
			return fmt.Errorf("init pass %d maintenance: %w", i+1, err)
		}
	}
	return nil
}
