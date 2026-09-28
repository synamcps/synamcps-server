package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
)

type Entry struct {
	Name      string         `json:"name"`
	Cron      string         `json:"time"`
	Task      string         `json:"task"`
	Data      map[string]any `json:"data"`
	NextRunAt time.Time      `json:"nextRunAt"`
	Enabled   bool           `json:"enabled"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

type Delivery struct {
	ID        int64          `json:"id"`
	ObjectID  string         `json:"objectId"`
	EntryName string         `json:"entryName"`
	RunAt     time.Time      `json:"time"`
	Task      string         `json:"task"`
	Data      map[string]any `json:"data"`
}

type Service struct {
	mu  sync.Mutex
	mem map[string]map[string]Entry
	del map[string][]Delivery
}

func NewService() *Service {
	return &Service{mem: map[string]map[string]Entry{}, del: map[string][]Delivery{}}
}

func (s *Service) Set(ctx context.Context, scope *policy.Scope, name, cronExpr, task string, data map[string]any) (Entry, error) {
	if scope == nil || scope.Kind() != models.ObjectKindSchedule {
		return Entry{}, policy.ErrKindMismatch
	}
	if name == "" || cronExpr == "" || task == "" {
		return Entry{}, errors.New("name, time, and task required")
	}
	if data == nil {
		data = map[string]any{}
	}
	now := time.Now().UTC()
	next := nextFromCron(cronExpr, now)
	e := Entry{Name: name, Cron: cronExpr, Task: task, Data: data, NextRunAt: next, Enabled: true, UpdatedAt: now}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.mem[scope.ObjectID()] == nil {
			s.mem[scope.ObjectID()] = map[string]Entry{}
		}
		s.mem[scope.ObjectID()][name] = e
		return e, scope.Commit(ctx)
	}
	raw, _ := json.Marshal(data)
	_, err := scope.Tx().Exec(ctx, `INSERT INTO schedule_entries (object_id, name, cron_expr, task, data, next_run_at, enabled, updated_at)
VALUES ($1,$2,$3,$4,$5::jsonb,$6,true,$7)
ON CONFLICT (object_id, name) DO UPDATE SET cron_expr=EXCLUDED.cron_expr, task=EXCLUDED.task, data=EXCLUDED.data, next_run_at=EXCLUDED.next_run_at, enabled=true, updated_at=EXCLUDED.updated_at`,
		scope.ObjectID(), name, cronExpr, task, string(raw), next, now)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Entry{}, err
	}
	return e, scope.Commit(ctx)
}

func (s *Service) Get(ctx context.Context, scope *policy.Scope, name string) (Entry, bool, error) {
	if scope == nil || scope.Kind() != models.ObjectKindSchedule {
		return Entry{}, false, policy.ErrKindMismatch
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		e, ok := s.mem[scope.ObjectID()][name]
		_ = scope.Commit(ctx)
		return e, ok, nil
	}
	row := scope.Tx().QueryRow(ctx, `SELECT name, cron_expr, task, data::text, next_run_at, enabled, updated_at FROM schedule_entries WHERE object_id=$1 AND name=$2`,
		scope.ObjectID(), name)
	var e Entry
	var raw string
	if err := row.Scan(&e.Name, &e.Cron, &e.Task, &raw, &e.NextRunAt, &e.Enabled, &e.UpdatedAt); err != nil {
		_ = scope.Commit(ctx)
		return Entry{}, false, nil
	}
	e.Data = map[string]any{}
	_ = json.Unmarshal([]byte(raw), &e.Data)
	return e, true, scope.Commit(ctx)
}

func (s *Service) List(ctx context.Context, scope *policy.Scope) ([]Entry, error) {
	if scope == nil || scope.Kind() != models.ObjectKindSchedule {
		return nil, policy.ErrKindMismatch
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := []Entry{}
		for _, e := range s.mem[scope.ObjectID()] {
			out = append(out, e)
		}
		_ = scope.Commit(ctx)
		return out, nil
	}
	rows, err := scope.Tx().Query(ctx, `SELECT name, cron_expr, task, data::text, next_run_at, enabled, updated_at FROM schedule_entries WHERE object_id=$1 ORDER BY name`, scope.ObjectID())
	if err != nil {
		_ = scope.Rollback(ctx)
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var raw string
		if err := rows.Scan(&e.Name, &e.Cron, &e.Task, &raw, &e.NextRunAt, &e.Enabled, &e.UpdatedAt); err != nil {
			_ = scope.Rollback(ctx)
			return nil, err
		}
		e.Data = map[string]any{}
		_ = json.Unmarshal([]byte(raw), &e.Data)
		out = append(out, e)
	}
	return out, scope.Commit(ctx)
}

// Wait claims the next undelivered firing for the object (long-poll helper).
func (s *Service) Wait(ctx context.Context, objectID string, claimer string, poll func(context.Context) (Delivery, bool, error)) (Delivery, error) {
	for {
		select {
		case <-ctx.Done():
			return Delivery{}, ctx.Err()
		default:
		}
		d, ok, err := poll(ctx)
		if err != nil {
			return Delivery{}, err
		}
		if ok {
			return d, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (s *Service) ClaimMemory(objectID, claimer string) (Delivery, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.del[objectID]
	if len(q) == 0 {
		return Delivery{}, false
	}
	d := q[0]
	s.del[objectID] = q[1:]
	return d, true
}

// Claim claims the next undelivered firing inside an open policy scope (Postgres).
func (s *Service) Claim(ctx context.Context, scope *policy.Scope, claimer string) (Delivery, bool, error) {
	if scope == nil || scope.Kind() != models.ObjectKindSchedule {
		return Delivery{}, false, policy.ErrKindMismatch
	}
	if scope.Memory() || scope.Tx() == nil {
		d, ok := s.ClaimMemory(scope.ObjectID(), claimer)
		_ = scope.Commit(ctx)
		return d, ok, nil
	}
	row := scope.Tx().QueryRow(ctx, `
WITH next AS (
  SELECT id FROM schedule_deliveries
  WHERE object_id=$1 AND claimed_by IS NULL
  ORDER BY created_at ASC
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
UPDATE schedule_deliveries d SET claimed_by=$2, claimed_at=NOW()
FROM next WHERE d.id=next.id
RETURNING d.id, d.object_id, d.entry_name, d.run_at, d.task, d.data::text`,
		scope.ObjectID(), claimer)
	var d Delivery
	var raw string
	if err := row.Scan(&d.ID, &d.ObjectID, &d.EntryName, &d.RunAt, &d.Task, &raw); err != nil {
		_ = scope.Commit(ctx)
		return Delivery{}, false, nil
	}
	d.Data = map[string]any{}
	_ = json.Unmarshal([]byte(raw), &d.Data)
	return d, true, scope.Commit(ctx)
}

func (s *Service) PushMemory(d Delivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.del[d.ObjectID] = append(s.del[d.ObjectID], d)
}

// nextFromCron is a minimal scheduler: supports "@every Ns/Nm/Nh" and falls back to +1m.
func nextFromCron(expr string, from time.Time) time.Time {
	if len(expr) > 7 && expr[:6] == "@every" {
		d, err := time.ParseDuration(expr[7:])
		if err == nil && d > 0 {
			return from.Add(d)
		}
	}
	return from.Add(time.Minute)
}

// FireDue advances due entries and returns deliveries (memory path used by worker).
func (s *Service) FireDueMemory(now time.Time) []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Delivery{}
	for objectID, entries := range s.mem {
		for name, e := range entries {
			if !e.Enabled || e.NextRunAt.After(now) {
				continue
			}
			d := Delivery{ObjectID: objectID, EntryName: name, RunAt: now, Task: e.Task, Data: e.Data}
			out = append(out, d)
			e.NextRunAt = nextFromCron(e.Cron, now)
			e.UpdatedAt = now
			entries[name] = e
			s.del[objectID] = append(s.del[objectID], d)
		}
	}
	return out
}
