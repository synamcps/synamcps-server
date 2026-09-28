package logstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
)

type BlobStore interface {
	Put(ctx context.Context, key string, data []byte) error
}

type Service struct {
	mu   sync.Mutex
	mem  map[string][]Entry
	blob BlobStore
}

type Entry struct {
	ID        int64          `json:"id"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

func NewService(blob BlobStore) *Service {
	return &Service{mem: map[string][]Entry{}, blob: blob}
}

func (s *Service) Add(ctx context.Context, scope *policy.Scope, level, message string, fields map[string]any) (Entry, error) {
	if scope == nil || scope.Kind() != models.ObjectKindLog {
		return Entry{}, policy.ErrKindMismatch
	}
	if message == "" {
		return Entry{}, errors.New("message required")
	}
	if level == "" {
		level = "info"
	}
	if fields == nil {
		fields = map[string]any{}
	}
	now := time.Now().UTC()
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		id := int64(len(s.mem[scope.ObjectID()]) + 1)
		e := Entry{ID: id, Level: level, Message: message, Fields: fields, CreatedAt: now}
		s.mem[scope.ObjectID()] = append(s.mem[scope.ObjectID()], e)
		return e, scope.Commit(ctx)
	}
	raw, _ := json.Marshal(fields)
	var id int64
	err := scope.Tx().QueryRow(ctx, `INSERT INTO log_entries (object_id, level, message, fields, created_at)
VALUES ($1,$2,$3,$4::jsonb,$5) RETURNING id`, scope.ObjectID(), level, message, string(raw), now).Scan(&id)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Entry{}, err
	}
	return Entry{ID: id, Level: level, Message: message, Fields: fields, CreatedAt: now}, scope.Commit(ctx)
}

func (s *Service) Tail(ctx context.Context, scope *policy.Scope, limit int) ([]Entry, error) {
	if scope == nil || scope.Kind() != models.ObjectKindLog {
		return nil, policy.ErrKindMismatch
	}
	if limit <= 0 {
		limit = 100
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		all := append([]Entry{}, s.mem[scope.ObjectID()]...)
		sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })
		if len(all) > limit {
			all = all[:limit]
		}
		_ = scope.Commit(ctx)
		return all, nil
	}
	rows, err := scope.Tx().Query(ctx, `SELECT id, level, message, fields::text, created_at FROM log_entries
WHERE object_id=$1 ORDER BY id DESC LIMIT $2`, scope.ObjectID(), limit)
	if err != nil {
		_ = scope.Rollback(ctx)
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var raw string
		if err := rows.Scan(&e.ID, &e.Level, &e.Message, &raw, &e.CreatedAt); err != nil {
			_ = scope.Rollback(ctx)
			return nil, err
		}
		e.Fields = map[string]any{}
		_ = json.Unmarshal([]byte(raw), &e.Fields)
		out = append(out, e)
	}
	return out, scope.Commit(ctx)
}

// RotateOlderThan archives hot rows older than cutoff to S3 and deletes them.
func (s *Service) RotateOlderThan(ctx context.Context, objectID string, cutoff time.Time, s3Prefix string, exec func(ctx context.Context, sql string, args ...any) error, query func(ctx context.Context, sql string, args ...any) ([]Entry, error)) error {
	if s.blob == nil {
		return nil
	}
	entries, err := query(ctx, `SELECT id, level, message, fields::text, created_at FROM log_entries WHERE object_id=$1 AND created_at < $2 ORDER BY id`, objectID, cutoff)
	if err != nil || len(entries) == 0 {
		return err
	}
	byDay := map[string][]Entry{}
	for _, e := range entries {
		day := e.CreatedAt.UTC().Format("2006-01-02")
		byDay[day] = append(byDay[day], e)
	}
	for day, list := range byDay {
		raw, _ := json.Marshal(list)
		key := fmt.Sprintf("%slogs/%s.jsonl", s3Prefix, day)
		if err := s.blob.Put(ctx, key, raw); err != nil {
			return err
		}
	}
	ids := make([]int64, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return exec(ctx, `DELETE FROM log_entries WHERE object_id=$1 AND id = ANY($2)`, objectID, ids)
}
