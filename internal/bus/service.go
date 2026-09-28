package bus

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
)

type Kafka interface {
	Produce(ctx context.Context, topic string, payload []byte) error
	Consume(ctx context.Context, topic, consumer string, max int) ([][]byte, error)
}

type Service struct {
	mu    sync.Mutex
	mem   map[string][]Event
	offs  map[string]map[string]int64
	kafka Kafka
}

type Event struct {
	ID        int64          `json:"id"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"createdAt"`
}

func NewService(k Kafka) *Service {
	return &Service{mem: map[string][]Event{}, offs: map[string]map[string]int64{}, kafka: k}
}

func (s *Service) Produce(ctx context.Context, scope *policy.Scope, payload map[string]any) (Event, error) {
	if scope == nil || scope.Kind() != models.ObjectKindBus {
		return Event{}, policy.ErrKindMismatch
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if scope.Object.Backend == "kafka" {
		if s.kafka == nil {
			return Event{}, errors.New("kafka backend not configured")
		}
		raw, _ := json.Marshal(payload)
		if err := s.kafka.Produce(ctx, "syna."+scope.ObjectID(), raw); err != nil {
			_ = scope.Rollback(ctx)
			return Event{}, err
		}
		_ = scope.Commit(ctx)
		return Event{Payload: payload, CreatedAt: time.Now().UTC()}, nil
	}
	now := time.Now().UTC()
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		id := int64(len(s.mem[scope.ObjectID()]) + 1)
		e := Event{ID: id, Payload: payload, CreatedAt: now}
		s.mem[scope.ObjectID()] = append(s.mem[scope.ObjectID()], e)
		return e, scope.Commit(ctx)
	}
	raw, _ := json.Marshal(payload)
	var id int64
	err := scope.Tx().QueryRow(ctx, `INSERT INTO bus_events (object_id, payload, created_at) VALUES ($1,$2::jsonb,$3) RETURNING id`,
		scope.ObjectID(), string(raw), now).Scan(&id)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Event{}, err
	}
	return Event{ID: id, Payload: payload, CreatedAt: now}, scope.Commit(ctx)
}

func (s *Service) Consume(ctx context.Context, scope *policy.Scope, consumer string, max int) ([]Event, error) {
	if scope == nil || scope.Kind() != models.ObjectKindBus {
		return nil, policy.ErrKindMismatch
	}
	if consumer == "" {
		consumer = "default"
	}
	if max <= 0 {
		max = 10
	}
	if scope.Object.Backend == "kafka" {
		if s.kafka == nil {
			return nil, errors.New("kafka backend not configured")
		}
		raws, err := s.kafka.Consume(ctx, "syna."+scope.ObjectID(), consumer, max)
		_ = scope.Commit(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]Event, 0, len(raws))
		for _, raw := range raws {
			payload := map[string]any{}
			_ = json.Unmarshal(raw, &payload)
			out = append(out, Event{Payload: payload, CreatedAt: time.Now().UTC()})
		}
		return out, nil
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.offs[scope.ObjectID()] == nil {
			s.offs[scope.ObjectID()] = map[string]int64{}
		}
		last := s.offs[scope.ObjectID()][consumer]
		out := []Event{}
		for _, e := range s.mem[scope.ObjectID()] {
			if e.ID > last {
				out = append(out, e)
				if len(out) >= max {
					break
				}
			}
		}
		if len(out) > 0 {
			s.offs[scope.ObjectID()][consumer] = out[len(out)-1].ID
		}
		_ = scope.Commit(ctx)
		return out, nil
	}
	var last int64
	_ = scope.Tx().QueryRow(ctx, `SELECT last_id FROM bus_offsets WHERE object_id=$1 AND consumer=$2`, scope.ObjectID(), consumer).Scan(&last)
	rows, err := scope.Tx().Query(ctx, `SELECT id, payload::text, created_at FROM bus_events WHERE object_id=$1 AND id > $2 ORDER BY id LIMIT $3`,
		scope.ObjectID(), last, max)
	if err != nil {
		_ = scope.Rollback(ctx)
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var raw string
		if err := rows.Scan(&e.ID, &raw, &e.CreatedAt); err != nil {
			_ = scope.Rollback(ctx)
			return nil, err
		}
		e.Payload = map[string]any{}
		_ = json.Unmarshal([]byte(raw), &e.Payload)
		out = append(out, e)
	}
	if len(out) > 0 {
		_, err = scope.Tx().Exec(ctx, `INSERT INTO bus_offsets (object_id, consumer, last_id) VALUES ($1,$2,$3)
ON CONFLICT (object_id, consumer) DO UPDATE SET last_id=EXCLUDED.last_id`, scope.ObjectID(), consumer, out[len(out)-1].ID)
		if err != nil {
			_ = scope.Rollback(ctx)
			return nil, err
		}
	}
	return out, scope.Commit(ctx)
}

// Subscribe polls Consume until ctx done; used by HTTP SSE / streaming MCP.
func (s *Service) Subscribe(ctx context.Context, scopeFactory func() (*policy.Scope, error), consumer string, emit func(Event) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		scope, err := scopeFactory()
		if err != nil {
			return err
		}
		events, err := s.Consume(ctx, scope, consumer, 10)
		if err != nil {
			return err
		}
		for _, e := range events {
			if err := emit(e); err != nil {
				return err
			}
		}
		if len(events) == 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}
}
