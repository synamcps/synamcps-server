package ingest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type JobKind string

const (
	JobKindIngest        JobKind = "ingest"
	JobKindDeleteVectors JobKind = "delete_vectors"
)

type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
)

type Job struct {
	ID          string
	DocID       string
	Kind        JobKind
	Status      JobStatus
	Attempts    int
	MaxAttempts int
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
}

type JobStore interface {
	Enqueue(ctx context.Context, docID string, kind JobKind, maxAttempts int) (Job, error)
	Claim(ctx context.Context) (Job, bool, error)
	MarkCompleted(ctx context.Context, jobID string) error
	MarkFailed(ctx context.Context, job Job, lastErr error) (retry bool, err error)
	RecoverStale(ctx context.Context, staleAfter time.Duration) error
	HasPending(ctx context.Context) (bool, error)
}

func NewJobStore(ctx context.Context, pool *pgxpool.Pool) JobStore {
	if pool == nil {
		return NewInMemoryJobStore()
	}
	return &postgresJobStore{pool: pool}
}

type InMemoryJobStore struct {
	mu    sync.Mutex
	jobs  map[string]Job
	wake  chan struct{}
	seq   int
}

func NewInMemoryJobStore() *InMemoryJobStore {
	return &InMemoryJobStore{
		jobs: map[string]Job{},
		wake: make(chan struct{}, 1),
	}
}

func (s *InMemoryJobStore) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *InMemoryJobStore) Enqueue(_ context.Context, docID string, kind JobKind, maxAttempts int) (Job, error) {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.DocID == docID && j.Kind == kind && (j.Status == JobStatusPending || j.Status == JobStatusRunning) {
			return j, nil
		}
	}
	s.seq++
	job := Job{
		ID:          fmt.Sprintf("job-%d", s.seq),
		DocID:       docID,
		Kind:        kind,
		Status:      JobStatusPending,
		MaxAttempts: maxAttempts,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.jobs[job.ID] = job
	s.notify()
	return job, nil
}

func (s *InMemoryJobStore) Claim(_ context.Context) (Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending []Job
	for _, j := range s.jobs {
		if j.Status == JobStatusPending && j.Attempts < j.MaxAttempts {
			pending = append(pending, j)
		}
	}
	if len(pending) == 0 {
		return Job{}, false, nil
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt.Before(pending[j].CreatedAt) })
	job := pending[0]
	now := time.Now().UTC()
	job.Status = JobStatusRunning
	job.Attempts++
	job.UpdatedAt = now
	job.StartedAt = &now
	s.jobs[job.ID] = job
	return job, true, nil
}

func (s *InMemoryJobStore) MarkCompleted(_ context.Context, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return fmt.Errorf("job %q not found", jobID)
	}
	now := time.Now().UTC()
	job.Status = JobStatusCompleted
	job.UpdatedAt = now
	job.CompletedAt = &now
	s.jobs[jobID] = job
	return nil
}

func (s *InMemoryJobStore) MarkFailed(_ context.Context, job Job, lastErr error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.jobs[job.ID]
	if !ok {
		return false, fmt.Errorf("job %q not found", job.ID)
	}
	now := time.Now().UTC()
	cur.LastError = lastErr.Error()
	cur.UpdatedAt = now
	if cur.Attempts >= cur.MaxAttempts {
		cur.Status = JobStatusFailed
		s.jobs[job.ID] = cur
		return false, nil
	}
	cur.Status = JobStatusPending
	s.jobs[job.ID] = cur
	s.notify()
	return true, nil
}

func (s *InMemoryJobStore) RecoverStale(_ context.Context, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for id, j := range s.jobs {
		if j.Status == JobStatusRunning {
			j.Status = JobStatusPending
			j.UpdatedAt = now
			s.jobs[id] = j
		}
	}
	return nil
}

func (s *InMemoryJobStore) HasPending(_ context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.Status == JobStatusPending && j.Attempts < j.MaxAttempts {
			return true, nil
		}
	}
	return false, nil
}

func (s *InMemoryJobStore) Wake() <-chan struct{} {
	return s.wake
}

type postgresJobStore struct {
	pool *pgxpool.Pool
}

func (s *postgresJobStore) Enqueue(ctx context.Context, docID string, kind JobKind, maxAttempts int) (Job, error) {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	row := s.pool.QueryRow(ctx, `
INSERT INTO knowledge_ingest_jobs (id, doc_id, kind, status, max_attempts, created_at, updated_at)
VALUES ($1, $2, $3, 'pending', $4, $5, $5)
ON CONFLICT DO NOTHING
RETURNING id, doc_id, kind, status, attempts, max_attempts, last_error, created_at, updated_at, started_at, completed_at`,
		id, docID, string(kind), maxAttempts, now)
	job, err := scanJob(row)
	if err == nil {
		return job, nil
	}
	if err != pgx.ErrNoRows {
		return Job{}, err
	}
	// Active job already exists for this doc/kind.
	rows, err := s.pool.Query(ctx, `
SELECT id, doc_id, kind, status, attempts, max_attempts, last_error, created_at, updated_at, started_at, completed_at
FROM knowledge_ingest_jobs
WHERE doc_id = $1 AND kind = $2 AND status IN ('pending', 'running')
ORDER BY created_at DESC
LIMIT 1`, docID, string(kind))
	if err != nil {
		return Job{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Job{}, fmt.Errorf("enqueue conflict for doc %q", docID)
	}
	return scanJob(rows)
}

func (s *postgresJobStore) Claim(ctx context.Context) (Job, bool, error) {
	row := s.pool.QueryRow(ctx, `
UPDATE knowledge_ingest_jobs
SET status = 'running', attempts = attempts + 1, started_at = NOW(), updated_at = NOW()
WHERE id = (
  SELECT id FROM knowledge_ingest_jobs
  WHERE status = 'pending' AND attempts < max_attempts
  ORDER BY created_at
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
RETURNING id, doc_id, kind, status, attempts, max_attempts, last_error, created_at, updated_at, started_at, completed_at`)
	job, err := scanJob(row)
	if err == pgx.ErrNoRows {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

func (s *postgresJobStore) MarkCompleted(ctx context.Context, jobID string) error {
	_, err := s.pool.Exec(ctx, `
UPDATE knowledge_ingest_jobs
SET status = 'completed', completed_at = NOW(), updated_at = NOW()
WHERE id = $1`, jobID)
	return err
}

func (s *postgresJobStore) MarkFailed(ctx context.Context, job Job, lastErr error) (bool, error) {
	msg := ""
	if lastErr != nil {
		msg = lastErr.Error()
	}
	if job.Attempts >= job.MaxAttempts {
		_, err := s.pool.Exec(ctx, `
UPDATE knowledge_ingest_jobs
SET status = 'failed', last_error = $2, updated_at = NOW()
WHERE id = $1`, job.ID, msg)
		return false, err
	}
	_, err := s.pool.Exec(ctx, `
UPDATE knowledge_ingest_jobs
SET status = 'pending', last_error = $2, updated_at = NOW()
WHERE id = $1`, job.ID, msg)
	return true, err
}

func (s *postgresJobStore) RecoverStale(ctx context.Context, staleAfter time.Duration) error {
	if staleAfter <= 0 {
		staleAfter = 5 * time.Minute
	}
	cutoff := time.Now().UTC().Add(-staleAfter)
	_, err := s.pool.Exec(ctx, `
UPDATE knowledge_ingest_jobs
SET status = 'pending', updated_at = NOW()
WHERE status = 'running' AND updated_at < $1`, cutoff)
	return err
}

func (s *postgresJobStore) HasPending(ctx context.Context) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
SELECT count(1) FROM knowledge_ingest_jobs
WHERE status = 'pending' AND attempts < max_attempts`).Scan(&n)
	return n > 0, err
}

type jobScanner interface {
	Scan(dest ...any) error
}

func scanJob(row jobScanner) (Job, error) {
	var job Job
	var kind, status string
	var startedAt, completedAt *time.Time
	err := row.Scan(
		&job.ID, &job.DocID, &kind, &status,
		&job.Attempts, &job.MaxAttempts, &job.LastError,
		&job.CreatedAt, &job.UpdatedAt, &startedAt, &completedAt,
	)
	if err != nil {
		return Job{}, err
	}
	job.Kind = JobKind(kind)
	job.Status = JobStatus(status)
	job.StartedAt = startedAt
	job.CompletedAt = completedAt
	return job, nil
}
