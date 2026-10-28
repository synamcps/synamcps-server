package securebus

import (
	"context"
	"encoding/json"
	"time"

	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
)

type ConsumeResult struct {
	Messages []Message `json:"messages"`
}

func (s *Service) Consume(ctx context.Context, scope *policy.Scope, consumer, conditionName string, adHoc map[string]any, max int) (ConsumeResult, error) {
	if scope == nil || scope.Kind() != models.ObjectKindSecureBus {
		return ConsumeResult{}, policy.ErrKindMismatch
	}
	if consumer == "" {
		consumer = "default"
	}
	if max <= 0 {
		max = 10
	}
	pol, err := s.currentPolicy(ctx, scope)
	if err != nil {
		_ = scope.Rollback(ctx)
		return ConsumeResult{}, err
	}
	cond, err := ResolveCondition(pol.Document, conditionName, adHoc)
	if err != nil {
		_ = scope.Rollback(ctx)
		return ConsumeResult{}, err
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.memSubs[scope.ObjectID()] == nil {
			s.memSubs[scope.ObjectID()] = map[string]Subscription{}
		}
		if s.memDelivered[scope.ObjectID()] == nil {
			s.memDelivered[scope.ObjectID()] = map[string]map[int64]struct{}{}
		}
		if s.memDelivered[scope.ObjectID()][consumer] == nil {
			s.memDelivered[scope.ObjectID()][consumer] = map[int64]struct{}{}
		}
		sub := s.memSubs[scope.ObjectID()][consumer]
		sub.Consumer = consumer
		if conditionName != "" {
			sub.Filter = map[string]any{"condition": conditionName}
		} else if adHoc != nil {
			sub.Filter = adHoc
		}
		out := []Message{}
		last := sub.LastEventID
		for _, ev := range s.memEvents[scope.ObjectID()] {
			if ev.ID <= last {
				continue
			}
			last = ev.ID
			if _, done := s.memDelivered[scope.ObjectID()][consumer][ev.MessageID]; done {
				continue
			}
			msg, ok := findMsg(s.memMessages[scope.ObjectID()], ev.MessageID)
			if !ok {
				continue
			}
			view := s.messageViewLocked(scope.ObjectID(), pol.Document, msg)
			if !MatchCondition(cond, view) {
				continue
			}
			out = append(out, msg)
			s.memDelivered[scope.ObjectID()][consumer][msg.ID] = struct{}{}
			if len(out) >= max {
				break
			}
		}
		sub.LastEventID = last
		s.memSubs[scope.ObjectID()][consumer] = sub
		_ = scope.Commit(ctx)
		return ConsumeResult{Messages: out}, nil
	}

	filterRaw, _ := json.Marshal(map[string]any{"condition": conditionName, "filter": adHoc})
	var last int64
	_ = scope.Tx().QueryRow(ctx, `SELECT last_event_id FROM securebus_subscriptions WHERE object_id=$1 AND consumer=$2`, scope.ObjectID(), consumer).Scan(&last)
	_, _ = scope.Tx().Exec(ctx, `INSERT INTO securebus_subscriptions (object_id, consumer, filter, last_event_id)
VALUES ($1,$2,$3::jsonb,$4) ON CONFLICT (object_id, consumer) DO UPDATE SET filter=EXCLUDED.filter`,
		scope.ObjectID(), consumer, string(filterRaw), last)

	rows, err := scope.Tx().Query(ctx, `SELECT id, kind, message_id FROM securebus_events WHERE object_id=$1 AND id > $2 ORDER BY id LIMIT $3`,
		scope.ObjectID(), last, max*5)
	if err != nil {
		_ = scope.Rollback(ctx)
		return ConsumeResult{}, err
	}
	type evRow struct {
		ID, MessageID int64
		Kind          string
	}
	evs := []evRow{}
	maxSeen := last
	for rows.Next() {
		var e evRow
		if err := rows.Scan(&e.ID, &e.Kind, &e.MessageID); err != nil {
			rows.Close()
			_ = scope.Rollback(ctx)
			return ConsumeResult{}, err
		}
		evs = append(evs, e)
		if e.ID > maxSeen {
			maxSeen = e.ID
		}
	}
	rows.Close()

	out := []Message{}
	for _, ev := range evs {
		var exists bool
		_ = scope.Tx().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM securebus_deliveries WHERE object_id=$1 AND consumer=$2 AND message_id=$3)`,
			scope.ObjectID(), consumer, ev.MessageID).Scan(&exists)
		if exists {
			continue
		}
		msg, err := s.getMessage(ctx, scope, ev.MessageID)
		if err != nil {
			continue
		}
		view, err := s.messageViewDB(ctx, scope, pol.Document, msg)
		if err != nil {
			_ = scope.Rollback(ctx)
			return ConsumeResult{}, err
		}
		if !MatchCondition(cond, view) {
			continue
		}
		_, _ = scope.Tx().Exec(ctx, `INSERT INTO securebus_deliveries (object_id, consumer, message_id) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			scope.ObjectID(), consumer, msg.ID)
		out = append(out, msg)
		if len(out) >= max {
			break
		}
	}
	_, err = scope.Tx().Exec(ctx, `UPDATE securebus_subscriptions SET last_event_id=$1 WHERE object_id=$2 AND consumer=$3`, maxSeen, scope.ObjectID(), consumer)
	if err != nil {
		_ = scope.Rollback(ctx)
		return ConsumeResult{}, err
	}
	return ConsumeResult{Messages: out}, scope.Commit(ctx)
}

func findMsg(msgs []Message, id int64) (Message, bool) {
	for _, m := range msgs {
		if m.ID == id {
			return m, true
		}
	}
	return Message{}, false
}

func (s *Service) messageViewLocked(objectID string, doc Document, msg Message) MessageView {
	author, _ := doc.AgentNameForKey(msg.AuthorKeyID)
	view := MessageView{AuthorKeyID: msg.AuthorKeyID, AuthorName: author}
	for _, a := range s.memAttests[objectID] {
		if a.MessageID != msg.ID || a.SupersededAt != nil {
			continue
		}
		name, _ := doc.AgentNameForKey(a.SignerKeyID)
		view.Attestations = append(view.Attestations, AttestationView{AgentName: name, KeyID: a.SignerKeyID, Status: a.Status})
	}
	return view
}

func (s *Service) messageViewDB(ctx context.Context, scope *policy.Scope, doc Document, msg Message) (MessageView, error) {
	author, _ := doc.AgentNameForKey(msg.AuthorKeyID)
	view := MessageView{AuthorKeyID: msg.AuthorKeyID, AuthorName: author}
	rows, err := scope.Tx().Query(ctx, `SELECT signer_key_id, status FROM securebus_attestations
WHERE object_id=$1 AND message_id=$2 AND superseded_at IS NULL`, scope.ObjectID(), msg.ID)
	if err != nil {
		return view, err
	}
	defer rows.Close()
	for rows.Next() {
		var keyID, status string
		if err := rows.Scan(&keyID, &status); err != nil {
			return view, err
		}
		name, _ := doc.AgentNameForKey(keyID)
		view.Attestations = append(view.Attestations, AttestationView{AgentName: name, KeyID: keyID, Status: status})
	}
	return view, rows.Err()
}

// Subscribe polls Consume until ctx done.
func (s *Service) Subscribe(ctx context.Context, scopeFactory func() (*policy.Scope, error), consumer, condition string, adHoc map[string]any, emit func(Message) error) error {
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
		res, err := s.Consume(ctx, scope, consumer, condition, adHoc, 10)
		if err != nil {
			return err
		}
		for _, m := range res.Messages {
			if err := emit(m); err != nil {
				return err
			}
		}
		if len(res.Messages) == 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}
}
