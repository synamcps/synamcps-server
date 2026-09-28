package queue

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
)

type AMQP interface {
	Enqueue(ctx context.Context, queueName string, payload []byte) error
	Dequeue(ctx context.Context, queueName string) ([]byte, bool, error)
	Peek(ctx context.Context, queueName string) ([]byte, bool, error)
	Size(ctx context.Context, queueName string) (int64, error)
}

type Service struct {
	mu   sync.Mutex
	mem  map[string][]Message
	amqp AMQP
}

type Message struct {
	ID        int64          `json:"id"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"createdAt"`
}

func NewService(amqp AMQP) *Service {
	return &Service{mem: map[string][]Message{}, amqp: amqp}
}

func (s *Service) Enqueue(ctx context.Context, scope *policy.Scope, payload map[string]any) (Message, error) {
	if scope == nil || scope.Kind() != models.ObjectKindQueue {
		return Message{}, policy.ErrKindMismatch
	}
	if payload == nil {
		payload = map[string]any{}
	}
	backend := scope.Object.Backend
	if backend == "rabbitmq" {
		if s.amqp == nil {
			return Message{}, errors.New("rabbitmq backend not configured")
		}
		raw, _ := json.Marshal(payload)
		if err := s.amqp.Enqueue(ctx, "syna."+scope.ObjectID(), raw); err != nil {
			_ = scope.Rollback(ctx)
			return Message{}, err
		}
		_ = scope.Commit(ctx)
		return Message{Payload: payload, CreatedAt: time.Now().UTC()}, nil
	}
	now := time.Now().UTC()
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		id := int64(len(s.mem[scope.ObjectID()]) + 1)
		m := Message{ID: id, Payload: payload, CreatedAt: now}
		s.mem[scope.ObjectID()] = append(s.mem[scope.ObjectID()], m)
		return m, scope.Commit(ctx)
	}
	raw, _ := json.Marshal(payload)
	var id int64
	err := scope.Tx().QueryRow(ctx, `INSERT INTO queue_messages (object_id, payload, visible_at, created_at)
VALUES ($1,$2::jsonb,NOW(),$3) RETURNING id`, scope.ObjectID(), string(raw), now).Scan(&id)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Message{}, err
	}
	return Message{ID: id, Payload: payload, CreatedAt: now}, scope.Commit(ctx)
}

func (s *Service) Dequeue(ctx context.Context, scope *policy.Scope) (Message, bool, error) {
	if scope == nil || scope.Kind() != models.ObjectKindQueue {
		return Message{}, false, policy.ErrKindMismatch
	}
	if scope.Object.Backend == "rabbitmq" {
		if s.amqp == nil {
			return Message{}, false, errors.New("rabbitmq backend not configured")
		}
		raw, ok, err := s.amqp.Dequeue(ctx, "syna."+scope.ObjectID())
		if err != nil || !ok {
			_ = scope.Commit(ctx)
			return Message{}, ok, err
		}
		payload := map[string]any{}
		_ = json.Unmarshal(raw, &payload)
		_ = scope.Commit(ctx)
		return Message{Payload: payload, CreatedAt: time.Now().UTC()}, true, nil
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		q := s.mem[scope.ObjectID()]
		if len(q) == 0 {
			_ = scope.Commit(ctx)
			return Message{}, false, nil
		}
		m := q[0]
		s.mem[scope.ObjectID()] = q[1:]
		return m, true, scope.Commit(ctx)
	}
	token := uuid.NewString()
	row := scope.Tx().QueryRow(ctx, `
WITH next AS (
  SELECT id FROM queue_messages
  WHERE object_id=$1 AND visible_at <= NOW() AND (locked_until IS NULL OR locked_until < NOW())
  ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE queue_messages q SET locked_until = NOW() + interval '30 seconds', lock_token=$2
FROM next WHERE q.id = next.id
RETURNING q.id, q.payload::text, q.created_at`, scope.ObjectID(), token)
	var m Message
	var raw string
	if err := row.Scan(&m.ID, &raw, &m.CreatedAt); err != nil {
		_ = scope.Commit(ctx)
		return Message{}, false, nil
	}
	m.Payload = map[string]any{}
	_ = json.Unmarshal([]byte(raw), &m.Payload)
	_, _ = scope.Tx().Exec(ctx, `DELETE FROM queue_messages WHERE id=$1`, m.ID)
	return m, true, scope.Commit(ctx)
}

func (s *Service) Peek(ctx context.Context, scope *policy.Scope) (Message, bool, error) {
	if scope == nil || scope.Kind() != models.ObjectKindQueue {
		return Message{}, false, policy.ErrKindMismatch
	}
	if scope.Object.Backend == "rabbitmq" {
		if s.amqp == nil {
			return Message{}, false, errors.New("rabbitmq backend not configured")
		}
		raw, ok, err := s.amqp.Peek(ctx, "syna."+scope.ObjectID())
		_ = scope.Commit(ctx)
		if err != nil || !ok {
			return Message{}, ok, err
		}
		payload := map[string]any{}
		_ = json.Unmarshal(raw, &payload)
		return Message{Payload: payload}, true, nil
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		q := s.mem[scope.ObjectID()]
		_ = scope.Commit(ctx)
		if len(q) == 0 {
			return Message{}, false, nil
		}
		return q[0], true, nil
	}
	row := scope.Tx().QueryRow(ctx, `SELECT id, payload::text, created_at FROM queue_messages
WHERE object_id=$1 AND visible_at <= NOW() AND (locked_until IS NULL OR locked_until < NOW())
ORDER BY id LIMIT 1`, scope.ObjectID())
	var m Message
	var raw string
	if err := row.Scan(&m.ID, &raw, &m.CreatedAt); err != nil {
		_ = scope.Commit(ctx)
		return Message{}, false, nil
	}
	m.Payload = map[string]any{}
	_ = json.Unmarshal([]byte(raw), &m.Payload)
	return m, true, scope.Commit(ctx)
}

func (s *Service) Size(ctx context.Context, scope *policy.Scope) (int64, error) {
	if scope == nil || scope.Kind() != models.ObjectKindQueue {
		return 0, policy.ErrKindMismatch
	}
	if scope.Object.Backend == "rabbitmq" {
		if s.amqp == nil {
			return 0, errors.New("rabbitmq backend not configured")
		}
		n, err := s.amqp.Size(ctx, "syna."+scope.ObjectID())
		_ = scope.Commit(ctx)
		return n, err
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		n := int64(len(s.mem[scope.ObjectID()]))
		_ = scope.Commit(ctx)
		return n, nil
	}
	var n int64
	err := scope.Tx().QueryRow(ctx, `SELECT COUNT(*) FROM queue_messages WHERE object_id=$1 AND (locked_until IS NULL OR locked_until < NOW())`, scope.ObjectID()).Scan(&n)
	if err != nil {
		_ = scope.Rollback(ctx)
		return 0, err
	}
	return n, scope.Commit(ctx)
}
