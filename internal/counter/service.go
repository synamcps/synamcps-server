package counter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
)

type Service struct {
	mu     sync.Mutex
	mem    map[string]float64
	redis  *redis.Client
	prefix string
}

func NewService(rdb *redis.Client, keyPrefix string) *Service {
	return &Service{mem: map[string]float64{}, redis: rdb, prefix: keyPrefix}
}

func (s *Service) tsKey(objectID string) string {
	p := s.prefix
	if p != "" {
		p += ":"
	}
	return p + "ts:counter:" + objectID
}

func (s *Service) Set(ctx context.Context, scope *policy.Scope, value float64) (float64, error) {
	if scope == nil || scope.Kind() != models.ObjectKindCounter {
		return 0, policy.ErrKindMismatch
	}
	if err := s.writeMeta(ctx, scope, value); err != nil {
		return 0, err
	}
	if s.redis != nil {
		key := s.tsKey(scope.ObjectID())
		_ = s.redis.Do(ctx, "TS.CREATE", key, "RETENTION", int64((365*24*time.Hour)/time.Millisecond)).Err()
		if err := s.redis.Do(ctx, "TS.ADD", key, "*", value).Err(); err != nil {
			_ = scope.Rollback(ctx)
			return 0, fmt.Errorf("redis timeseries: %w", err)
		}
	} else {
		s.mu.Lock()
		s.mem[scope.ObjectID()] = value
		s.mu.Unlock()
	}
	return value, scope.Commit(ctx)
}

func (s *Service) Get(ctx context.Context, scope *policy.Scope) (float64, error) {
	if scope == nil || scope.Kind() != models.ObjectKindCounter {
		return 0, policy.ErrKindMismatch
	}
	if s.redis != nil {
		res, err := s.redis.Do(ctx, "TS.GET", s.tsKey(scope.ObjectID())).Result()
		if err == nil {
			if arr, ok := res.([]any); ok && len(arr) >= 2 {
				_ = scope.Commit(ctx)
				return asFloat(arr[1]), nil
			}
		}
	}
	v, err := s.readMeta(ctx, scope)
	if err != nil {
		_ = scope.Rollback(ctx)
		return 0, err
	}
	return v, scope.Commit(ctx)
}

func (s *Service) Increase(ctx context.Context, scope *policy.Scope, delta float64) (float64, error) {
	return s.adjust(ctx, scope, delta)
}

func (s *Service) Decrease(ctx context.Context, scope *policy.Scope, delta float64) (float64, error) {
	return s.adjust(ctx, scope, -delta)
}

func (s *Service) adjust(ctx context.Context, scope *policy.Scope, delta float64) (float64, error) {
	if scope == nil || scope.Kind() != models.ObjectKindCounter {
		return 0, policy.ErrKindMismatch
	}
	if delta == 0 {
		delta = 1
	}
	cur, err := s.readMeta(ctx, scope)
	if err != nil {
		_ = scope.Rollback(ctx)
		return 0, err
	}
	next := cur + delta
	if err := s.writeMeta(ctx, scope, next); err != nil {
		return 0, err
	}
	if s.redis != nil {
		key := s.tsKey(scope.ObjectID())
		_ = s.redis.Do(ctx, "TS.CREATE", key, "RETENTION", int64((365*24*time.Hour)/time.Millisecond)).Err()
		if err := s.redis.Do(ctx, "TS.ADD", key, "*", next).Err(); err != nil {
			// Fallback: still keep postgres meta when module missing.
			_ = err
		}
	} else {
		s.mu.Lock()
		s.mem[scope.ObjectID()] = next
		s.mu.Unlock()
	}
	return next, scope.Commit(ctx)
}

func (s *Service) writeMeta(ctx context.Context, scope *policy.Scope, value float64) error {
	now := time.Now().UTC()
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		s.mem[scope.ObjectID()] = value
		s.mu.Unlock()
		return nil
	}
	_, err := scope.Tx().Exec(ctx, `INSERT INTO counter_meta (object_id, value, updated_at) VALUES ($1,$2,$3)
ON CONFLICT (object_id) DO UPDATE SET value=EXCLUDED.value, updated_at=EXCLUDED.updated_at`, scope.ObjectID(), value, now)
	if err != nil {
		_ = scope.Rollback(ctx)
		return err
	}
	return nil
}

func (s *Service) readMeta(ctx context.Context, scope *policy.Scope) (float64, error) {
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.mem[scope.ObjectID()], nil
	}
	var v float64
	err := scope.Tx().QueryRow(ctx, `SELECT value FROM counter_meta WHERE object_id=$1`, scope.ObjectID()).Scan(&v)
	if err != nil {
		return 0, nil
	}
	return v, nil
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case string:
		var f float64
		_, _ = fmt.Sscanf(t, "%f", &f)
		return f
	default:
		return 0
	}
}

var ErrNoTimeSeries = errors.New("redis timeseries unavailable")
