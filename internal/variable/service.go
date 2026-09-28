package variable

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
)

type Service struct {
	mu   sync.RWMutex
	mem  map[string]map[string]Entry // objectID -> prefix/name -> entry
}

type Entry struct {
	Name      string         `json:"name"`
	Prefix    string         `json:"prefix"`
	Payload   map[string]any `json:"payload"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

func NewService() *Service {
	return &Service{mem: map[string]map[string]Entry{}}
}

func key(prefix, name string) string { return prefix + "\x00" + name }

func (s *Service) Set(ctx context.Context, scope *policy.Scope, name, prefix string, payload map[string]any) (Entry, error) {
	if scope == nil || scope.Kind() != models.ObjectKindVariable {
		return Entry{}, policy.ErrKindMismatch
	}
	if name == "" {
		return Entry{}, errors.New("name required")
	}
	if payload == nil {
		payload = map[string]any{}
	}
	now := time.Now().UTC()
	entry := Entry{Name: name, Prefix: prefix, Payload: payload, UpdatedAt: now}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.mem[scope.ObjectID()] == nil {
			s.mem[scope.ObjectID()] = map[string]Entry{}
		}
		s.mem[scope.ObjectID()][key(prefix, name)] = entry
		return entry, scope.Commit(ctx)
	}
	raw, _ := json.Marshal(payload)
	_, err := scope.Tx().Exec(ctx, `INSERT INTO variable_entries (object_id, name, prefix, payload, updated_at)
VALUES ($1,$2,$3,$4::jsonb,$5)
ON CONFLICT (object_id, prefix, name) DO UPDATE SET payload=EXCLUDED.payload, updated_at=EXCLUDED.updated_at`,
		scope.ObjectID(), name, prefix, string(raw), now)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Entry{}, err
	}
	return entry, scope.Commit(ctx)
}

func (s *Service) Get(ctx context.Context, scope *policy.Scope, name, prefix string) (Entry, bool, error) {
	if scope == nil || scope.Kind() != models.ObjectKindVariable {
		return Entry{}, false, policy.ErrKindMismatch
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		e, ok := s.mem[scope.ObjectID()][key(prefix, name)]
		_ = scope.Commit(ctx)
		return e, ok, nil
	}
	row := scope.Tx().QueryRow(ctx, `SELECT name, prefix, payload::text, updated_at FROM variable_entries WHERE object_id=$1 AND prefix=$2 AND name=$3`,
		scope.ObjectID(), prefix, name)
	var e Entry
	var raw string
	if err := row.Scan(&e.Name, &e.Prefix, &raw, &e.UpdatedAt); err != nil {
		_ = scope.Commit(ctx)
		return Entry{}, false, nil
	}
	e.Payload = map[string]any{}
	_ = json.Unmarshal([]byte(raw), &e.Payload)
	return e, true, scope.Commit(ctx)
}
