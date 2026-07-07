package ingest

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const defaultMaxAttempts = 3

type WorkerConfig struct {
	PollInterval time.Duration
	MaxAttempts  int
	StaleAfter   time.Duration
}

func (c WorkerConfig) maxAttempts() int {
	if c.MaxAttempts <= 0 {
		return defaultMaxAttempts
	}
	return c.MaxAttempts
}

func (c WorkerConfig) pollInterval() time.Duration {
	if c.PollInterval <= 0 {
		return 500 * time.Millisecond
	}
	return c.PollInterval
}

func (c WorkerConfig) staleAfter() time.Duration {
	if c.StaleAfter <= 0 {
		return 5 * time.Minute
	}
	return c.StaleAfter
}

type Worker struct {
	pipeline *Pipeline
	jobs     JobStore
	cfg      WorkerConfig
	wake     <-chan struct{}

	mu     sync.Mutex
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func NewWorker(pipeline *Pipeline, jobs JobStore, cfg WorkerConfig) *Worker {
	w := &Worker{
		pipeline: pipeline,
		jobs:     jobs,
		cfg:      cfg,
	}
	if mem, ok := jobs.(*InMemoryJobStore); ok {
		w.wake = mem.Wake()
	}
	return w
}

func (w *Worker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.wg.Add(1)
	go w.loop(runCtx)
}

func (w *Worker) Shutdown() {
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
	}
	w.mu.Unlock()
	w.wg.Wait()
}

func (w *Worker) EnqueueDelete(ctx context.Context, docID string) error {
	_, err := w.jobs.Enqueue(ctx, docID, JobKindDeleteVectors, w.cfg.maxAttempts())
	return err
}

// ProcessUntilIdle runs pending jobs synchronously; intended for tests.
func (w *Worker) ProcessUntilIdle(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		job, ok, err := w.jobs.Claim(ctx)
		if err != nil {
			return err
		}
		if !ok {
			pending, err := w.jobs.HasPending(ctx)
			if err != nil {
				return err
			}
			if !pending {
				return nil
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		w.handleJob(ctx, job)
	}
}

func (w *Worker) loop(ctx context.Context) {
	defer w.wg.Done()
	if err := w.jobs.RecoverStale(ctx, w.cfg.staleAfter()); err != nil {
		slog.Warn("ingest worker: recover stale jobs", "error", err)
	}
	ticker := time.NewTicker(w.cfg.pollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		job, ok, err := w.jobs.Claim(ctx)
		if err != nil {
			slog.Warn("ingest worker: claim job", "error", err)
		} else if ok {
			w.handleJob(ctx, job)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-ticker.C:
		}
	}
}

func (w *Worker) handleJob(ctx context.Context, job Job) {
	var err error
	switch job.Kind {
	case JobKindIngest:
		err = w.pipeline.ProcessIngest(ctx, job.DocID)
	case JobKindDeleteVectors:
		err = w.pipeline.ProcessDeleteVectors(ctx, job.DocID)
	default:
		err = w.pipeline.markJobFailed(ctx, job, errUnknownJobKind(job.Kind))
	}
	if err == nil {
		if markErr := w.jobs.MarkCompleted(ctx, job.ID); markErr != nil {
			slog.Warn("ingest worker: mark completed", "job", job.ID, "error", markErr)
		}
		return
	}
	slog.Warn("ingest worker: job failed", "job", job.ID, "kind", job.Kind, "doc", job.DocID, "error", err)
	retry, markErr := w.jobs.MarkFailed(ctx, job, err)
	if markErr != nil {
		slog.Warn("ingest worker: mark failed", "job", job.ID, "error", markErr)
		return
	}
	if !retry {
		w.pipeline.onJobTerminalFailure(ctx, job, err)
	}
}

type unknownJobKindErr JobKind

func (e unknownJobKindErr) Error() string {
	return "unknown ingest job kind: " + string(e)
}

func errUnknownJobKind(kind JobKind) error {
	return unknownJobKindErr(kind)
}
