package agent

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/config"
	"github.com/synamcps/synamcps-server/internal/domainerr"
	"github.com/synamcps/synamcps-server/internal/knowledge"
	"github.com/synamcps/synamcps-server/internal/llm"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/usage"
)

type Service struct {
	cfg       config.AgentConfig
	store     *Store
	knowledge *knowledge.Service
	access    *access.Service
	chat      llm.ChatModel
	usage     *usage.Service
}

func NewService(cfg config.AgentConfig, store *Store, knowledgeService *knowledge.Service, accessService *access.Service, chat llm.ChatModel, usageService *usage.Service) (*Service, error) {
	if store == nil {
		return nil, errors.New("agent store is required")
	}
	if knowledgeService == nil {
		return nil, errors.New("knowledge service is required")
	}
	if accessService == nil {
		return nil, errors.New("access service is required")
	}
	if chat == nil {
		chat = llm.NewSimpleChatModel(cfg)
	}
	return &Service{cfg: cfg, store: store, knowledge: knowledgeService, access: accessService, chat: chat, usage: usageService}, nil
}

func (s *Service) CreateConversation(ctx context.Context, p models.Principal, ac models.APIAccessContext, in CreateConversationInput) (Conversation, error) {
	datasets, err := s.resolveDatasets(ctx, p, ac, in.DatasetStorageIDs)
	if err != nil {
		return Conversation{}, err
	}
	if len(datasets) == 0 {
		return Conversation{}, domainerr.ErrForbidden
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "New conversation"
	}
	return s.store.CreateConversation(ctx, Conversation{
		OwnerSubjectKey:   models.SubjectKeyForPrincipal(p),
		Title:             title,
		DatasetStorageIDs: datasets,
	})
}

func (s *Service) ListConversations(ctx context.Context, p models.Principal) ([]Conversation, error) {
	return s.store.ListConversations(ctx, models.SubjectKeyForPrincipal(p))
}

func (s *Service) Messages(ctx context.Context, p models.Principal, conversationID string) ([]Message, error) {
	conv, err := s.requireConversation(ctx, p, conversationID)
	if err != nil {
		return nil, err
	}
	return s.store.ListMessages(ctx, conv.ID, 0)
}

func (s *Service) SendMessage(ctx context.Context, p models.Principal, ac models.APIAccessContext, conversationID string, in SendMessageInput) (Reply, error) {
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return Reply{}, errors.New("message content is required")
	}
	conv, err := s.requireConversation(ctx, p, conversationID)
	if err != nil {
		return Reply{}, err
	}
	datasets, err := s.resolveDatasets(ctx, p, ac, conv.DatasetStorageIDs)
	if err != nil {
		return Reply{}, err
	}
	if len(datasets) == 0 {
		return Reply{}, domainerr.ErrForbidden
	}
	if _, err := s.store.AppendMessage(ctx, Message{ConversationID: conv.ID, Role: "user", Content: content}); err != nil {
		return Reply{}, err
	}

	var saved *DocumentRef
	if shouldSaveMemory(content) {
		doc, err := s.knowledge.Save(ctx, p, ac, knowledge.SaveInput{
			StorageID:  datasets[0],
			Title:      memoryTitle(content),
			Text:       stripMemoryIntent(content),
			MimeType:   "text/plain",
			Visibility: models.VisibilityPersonal,
			Source:     "agent",
			Channel:    "agent",
		})
		if err != nil {
			return Reply{}, err
		}
		saved = &DocumentRef{DocID: doc.DocID, Title: doc.Title, UIHref: "/app/knowledge/" + doc.DocID}
	}

	hits, refs := s.searchDatasets(ctx, p, ac, datasets, content)
	history, err := s.store.ListMessages(ctx, conv.ID, s.cfg.MaxConversationMessages)
	if err != nil {
		return Reply{}, err
	}
	chatResp, err := s.chat.Generate(ctx, llm.ChatRequest{
		SystemPrompt: s.cfg.SystemPrompt,
		Messages:     toChatMessages(history),
		Documents:    toChatDocuments(hits, s.cfg.MaxContextChars),
		MaxTokens:    s.cfg.MaxResponseTokens,
		Tools:        allowedTools(s.cfg.AllowedTools),
	})
	if err != nil {
		return Reply{}, err
	}
	if saved != nil {
		chatResp.Content = strings.TrimSpace(chatResp.Content + "\n\nSaved new memory: " + saved.DocID)
	}
	msg, err := s.store.AppendMessage(ctx, Message{
		ConversationID: conv.ID,
		Role:           "assistant",
		Content:        chatResp.Content,
		Model:          chatResp.Model,
		DocumentRefs:   refs,
		InputTokens:    chatResp.InputTokens,
		OutputTokens:   chatResp.OutputTokens,
	})
	if err != nil {
		return Reply{}, err
	}
	s.recordUsage(ctx, p, ac, msg)
	updated, _, _ := s.store.GetConversation(ctx, conv.ID)
	if updated.ID != "" {
		conv = updated
	}
	return Reply{Conversation: conv, Message: msg, DocumentRefs: refs, SavedMemory: saved}, nil
}

func (s *Service) requireConversation(ctx context.Context, p models.Principal, id string) (Conversation, error) {
	conv, ok, err := s.store.GetConversation(ctx, id)
	if err != nil {
		return Conversation{}, err
	}
	if !ok {
		return Conversation{}, domainerr.ErrNotFound
	}
	if conv.OwnerSubjectKey != models.SubjectKeyForPrincipal(p) {
		return Conversation{}, domainerr.ErrForbidden
	}
	return conv, nil
}

func (s *Service) resolveDatasets(ctx context.Context, p models.Principal, ac models.APIAccessContext, requested []string) ([]string, error) {
	readable, err := s.access.ReadableStorageIDs(ctx, p, ac.AccessToken, ac.AllowedStorage)
	if err != nil {
		return nil, err
	}
	if len(requested) == 0 {
		out := make([]string, 0, len(readable))
		for id := range readable {
			out = append(out, id)
		}
		sort.Strings(out)
		return out, nil
	}
	out := []string{}
	seen := map[string]struct{}{}
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if _, ok := readable[id]; !ok {
			return nil, domainerr.ErrForbidden
		}
		out = append(out, id)
	}
	return out, nil
}

func (s *Service) searchDatasets(ctx context.Context, p models.Principal, ac models.APIAccessContext, datasets []string, query string) ([]models.SearchHit, []DocumentRef) {
	limit := s.cfg.MaxContextDocuments
	if limit <= 0 {
		limit = 5
	}
	all := []models.SearchHit{}
	for _, storageID := range datasets {
		hits, err := s.knowledge.Search(ctx, p, ac, models.SearchRequest{
			Query: query,
			TopK:  limit,
			Filters: models.PageRequest{
				StorageID: storageID,
			},
		}, false)
		if err != nil {
			continue
		}
		all = append(all, hits...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	if len(all) > limit {
		all = all[:limit]
	}
	refs := make([]DocumentRef, 0, len(all))
	seen := map[string]struct{}{}
	for _, hit := range all {
		if _, ok := seen[hit.DocID]; ok {
			continue
		}
		seen[hit.DocID] = struct{}{}
		refs = append(refs, DocumentRef{
			DocID:   hit.DocID,
			Title:   hit.Title,
			Score:   hit.Score,
			UIHref:  "/app/knowledge/" + hit.DocID,
			Snippet: hit.Snippet,
		})
	}
	return all, refs
}

func (s *Service) recordUsage(ctx context.Context, p models.Principal, ac models.APIAccessContext, msg Message) {
	if s.usage == nil {
		return
	}
	s.usage.Record(ctx, models.UsageEvent{
		TokenID:        ac.TokenID,
		UserSubjectKey: models.SubjectKeyForPrincipal(p),
		Tool:           "agent_chat",
		Operation:      "llm.generate",
		Status:         "ok",
		BytesIn:        int64(msg.InputTokens),
		BytesOut:       int64(msg.OutputTokens),
	})
}

func toChatMessages(messages []Message) []llm.ChatMessage {
	out := make([]llm.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, llm.ChatMessage{Role: msg.Role, Content: msg.Content})
	}
	return out
}

func toChatDocuments(hits []models.SearchHit, maxChars int) []llm.ChatDocument {
	if maxChars <= 0 {
		maxChars = 6000
	}
	out := []llm.ChatDocument{}
	used := 0
	for _, hit := range hits {
		snippet := strings.TrimSpace(hit.Snippet)
		if snippet == "" {
			continue
		}
		if used+len(snippet) > maxChars {
			remaining := maxChars - used
			if remaining <= 0 {
				break
			}
			snippet = snippet[:remaining]
		}
		used += len(snippet)
		out = append(out, llm.ChatDocument{DocID: hit.DocID, Title: hit.Title, Snippet: snippet, Score: hit.Score})
	}
	return out
}

func allowedTools(names []string) []llm.ChatTool {
	out := make([]llm.ChatTool, 0, len(names))
	for _, name := range names {
		out = append(out, llm.ChatTool{Name: name, Description: "SynaMCPs " + name})
	}
	return out
}

func shouldSaveMemory(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "remember ") || strings.Contains(lower, "запомни")
}

func stripMemoryIntent(content string) string {
	content = strings.TrimSpace(content)
	for _, prefix := range []string{"remember", "Remember", "запомни", "Запомни"} {
		if strings.HasPrefix(content, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(content, prefix))
		}
	}
	return content
}

func memoryTitle(content string) string {
	words := strings.Fields(stripMemoryIntent(content))
	if len(words) > 8 {
		words = words[:8]
	}
	if len(words) == 0 {
		return "Agent memory"
	}
	return strings.Join(words, " ")
}
