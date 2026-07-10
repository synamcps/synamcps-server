package agent

import (
	"context"
	"testing"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/config"
	"github.com/synamcps/synamcps-server/internal/domainerr"
	"github.com/synamcps/synamcps-server/internal/models"
)

func TestCreateConversationRejectsInaccessibleDataset(t *testing.T) {
	ctx := context.Background()
	p := testPrincipal()
	accessStore := access.NewInMemoryStore()
	accessService := access.NewService(accessStore)

	_, err := accessStore.CreateStorage(ctx, models.Storage{
		ID:              "allowed",
		OwnerSubjectKey: models.SubjectKeyForPrincipal(p),
		Visibility:      models.VisibilityPersonal,
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = accessStore.CreateStorage(ctx, models.Storage{
		ID:              "other",
		OwnerSubjectKey: "user:internal:bob",
		Visibility:      models.VisibilityPersonal,
	}, "test")
	if err != nil {
		t.Fatal(err)
	}

	svc := &Service{cfg: config.AgentConfig{}, store: NewInMemoryStore(), access: accessService}
	_, err = svc.CreateConversation(ctx, p, models.APIAccessContext{}, CreateConversationInput{
		DatasetStorageIDs: []string{"allowed", "other"},
	})
	if err == nil {
		t.Fatal("expected inaccessible dataset to be rejected")
	}
	if domainerr.HTTPStatus(err) != 403 {
		t.Fatalf("expected 403, got %v", err)
	}
}

func TestCreateConversationDefaultsToReadableDatasets(t *testing.T) {
	ctx := context.Background()
	p := testPrincipal()
	accessStore := access.NewInMemoryStore()
	accessService := access.NewService(accessStore)
	for _, id := range []string{"a", "b"} {
		_, err := accessStore.CreateStorage(ctx, models.Storage{
			ID:              id,
			OwnerSubjectKey: models.SubjectKeyForPrincipal(p),
			Visibility:      models.VisibilityPersonal,
		}, "test")
		if err != nil {
			t.Fatal(err)
		}
	}

	svc := &Service{cfg: config.AgentConfig{}, store: NewInMemoryStore(), access: accessService}
	conv, err := svc.CreateConversation(ctx, p, models.APIAccessContext{}, CreateConversationInput{})
	if err != nil {
		t.Fatal(err)
	}
	if got := conv.DatasetStorageIDs; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("unexpected datasets: %#v", got)
	}
}

func TestConversationOwnershipRequiredForMessages(t *testing.T) {
	ctx := context.Background()
	owner := testPrincipal()
	other := models.Principal{UserID: "bob", SubjectKey: "user:internal:bob", AuthSource: "internal"}
	store := NewInMemoryStore()
	conv, err := store.CreateConversation(ctx, Conversation{
		OwnerSubjectKey:   models.SubjectKeyForPrincipal(owner),
		Title:             "owned",
		DatasetStorageIDs: []string{"a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{store: store}
	_, err = svc.Messages(ctx, other, conv.ID)
	if err == nil {
		t.Fatal("expected forbidden for non-owner")
	}
	if domainerr.HTTPStatus(err) != 403 {
		t.Fatalf("expected 403, got %v", err)
	}
}

func testPrincipal() models.Principal {
	return models.Principal{UserID: "alice", SubjectKey: "user:internal:alice", AuthSource: "internal"}
}
