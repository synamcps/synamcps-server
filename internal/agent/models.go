package agent

import "time"

type Conversation struct {
	ID                string    `json:"id"`
	OwnerSubjectKey   string    `json:"ownerSubjectKey"`
	Title             string    `json:"title"`
	DatasetStorageIDs []string  `json:"datasetStorageIds"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type Message struct {
	ID             string        `json:"id"`
	ConversationID string        `json:"conversationId"`
	Role           string        `json:"role"`
	Content        string        `json:"content"`
	Model          string        `json:"model,omitempty"`
	DocumentRefs   []DocumentRef `json:"documentRefs,omitempty"`
	InputTokens    int           `json:"inputTokens,omitempty"`
	OutputTokens   int           `json:"outputTokens,omitempty"`
	CreatedAt      time.Time     `json:"createdAt"`
}

type DocumentRef struct {
	DocID   string  `json:"docId"`
	Title   string  `json:"title"`
	Score   float64 `json:"score"`
	UIHref  string  `json:"uiHref"`
	Snippet string  `json:"snippet,omitempty"`
}

type CreateConversationInput struct {
	Title             string   `json:"title"`
	DatasetStorageIDs []string `json:"datasetStorageIds"`
}

type SendMessageInput struct {
	Content string `json:"content"`
}

type Reply struct {
	Conversation Conversation  `json:"conversation"`
	Message      Message       `json:"message"`
	DocumentRefs []DocumentRef `json:"documentRefs"`
	SavedMemory  *DocumentRef  `json:"savedMemory,omitempty"`
}
