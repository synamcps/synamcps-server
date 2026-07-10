package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	mu            sync.RWMutex
	conversations map[string]Conversation
	messages      map[string][]Message
	pool          *pgxpool.Pool
	useDB         bool
}

func NewStore(pool *pgxpool.Pool) *Store {
	s := NewInMemoryStore()
	if pool != nil {
		s.pool = pool
		s.useDB = true
	}
	return s
}

func NewInMemoryStore() *Store {
	return &Store{
		conversations: map[string]Conversation{},
		messages:      map[string][]Message{},
	}
}

func (s *Store) CreateConversation(ctx context.Context, c Conversation) (Conversation, error) {
	now := time.Now().UTC()
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Status == "" {
		c.Status = "active"
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if s.useDB {
		_, err := s.pool.Exec(ctx, `
INSERT INTO agent_conversations (id, owner_subject_key, title, dataset_storage_ids, status, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			c.ID, c.OwnerSubjectKey, c.Title, c.DatasetStorageIDs, c.Status, c.CreatedAt, c.UpdatedAt)
		return c, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversations[c.ID] = c
	return c, nil
}

func (s *Store) GetConversation(ctx context.Context, id string) (Conversation, bool, error) {
	if s.useDB {
		row := s.pool.QueryRow(ctx, `
SELECT id, owner_subject_key, title, dataset_storage_ids, status, created_at, updated_at
FROM agent_conversations WHERE id=$1 AND status <> 'archived'`, id)
		var c Conversation
		if err := row.Scan(&c.ID, &c.OwnerSubjectKey, &c.Title, &c.DatasetStorageIDs, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Conversation{}, false, nil
			}
			return Conversation{}, false, err
		}
		return c, true, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.conversations[id]
	if ok && c.Status == "archived" {
		return Conversation{}, false, nil
	}
	return c, ok, nil
}

func (s *Store) ListConversations(ctx context.Context, ownerSubjectKey string) ([]Conversation, error) {
	if s.useDB {
		rows, err := s.pool.Query(ctx, `
SELECT id, owner_subject_key, title, dataset_storage_ids, status, created_at, updated_at
FROM agent_conversations
WHERE owner_subject_key=$1 AND status <> 'archived'
ORDER BY updated_at DESC`, ownerSubjectKey)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Conversation
		for rows.Next() {
			var c Conversation
			if err := rows.Scan(&c.ID, &c.OwnerSubjectKey, &c.Title, &c.DatasetStorageIDs, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Conversation{}
	for _, c := range s.conversations {
		if c.OwnerSubjectKey == ownerSubjectKey && c.Status != "archived" {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (s *Store) AppendMessage(ctx context.Context, msg Message) (Message, error) {
	now := time.Now().UTC()
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = now
	}
	if s.useDB {
		refs, err := json.Marshal(msg.DocumentRefs)
		if err != nil {
			return Message{}, err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return Message{}, err
		}
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, `
INSERT INTO agent_messages (id, conversation_id, role, content, model, document_refs_json, input_tokens, output_tokens, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			msg.ID, msg.ConversationID, msg.Role, msg.Content, msg.Model, string(refs), msg.InputTokens, msg.OutputTokens, msg.CreatedAt)
		if err != nil {
			return Message{}, err
		}
		_, err = tx.Exec(ctx, `UPDATE agent_conversations SET updated_at=$1 WHERE id=$2`, now, msg.ConversationID)
		if err != nil {
			return Message{}, err
		}
		return msg, tx.Commit(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages[msg.ConversationID] = append(s.messages[msg.ConversationID], msg)
	if c, ok := s.conversations[msg.ConversationID]; ok {
		c.UpdatedAt = now
		s.conversations[msg.ConversationID] = c
	}
	return msg, nil
}

func (s *Store) ListMessages(ctx context.Context, conversationID string, limit int) ([]Message, error) {
	if s.useDB {
		query := `
SELECT id, conversation_id, role, content, model, document_refs_json, input_tokens, output_tokens, created_at
FROM agent_messages WHERE conversation_id=$1 ORDER BY created_at ASC`
		args := []any{conversationID}
		if limit > 0 {
			query = `
SELECT id, conversation_id, role, content, model, document_refs_json, input_tokens, output_tokens, created_at
FROM (
  SELECT * FROM agent_messages WHERE conversation_id=$1 ORDER BY created_at DESC LIMIT $2
) recent ORDER BY created_at ASC`
			args = append(args, limit)
		}
		rows, err := s.pool.Query(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Message
		for rows.Next() {
			var msg Message
			var refs string
			if err := rows.Scan(&msg.ID, &msg.ConversationID, &msg.Role, &msg.Content, &msg.Model, &refs, &msg.InputTokens, &msg.OutputTokens, &msg.CreatedAt); err != nil {
				return nil, err
			}
			_ = json.Unmarshal([]byte(refs), &msg.DocumentRefs)
			out = append(out, msg)
		}
		return out, rows.Err()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]Message(nil), s.messages[conversationID]...)
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}
