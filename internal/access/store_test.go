package access

import (
	"context"
	"testing"
	"time"

	"github.com/synamcps/synamcps-server/internal/models"
)

func TestListAccessibleStoragesMemory(t *testing.T) {
	store := NewInMemoryStore()
	now := time.Now().UTC()
	seedStorage := func(id, owner string) {
		store.storages[id] = models.Storage{
			ID:              id,
			Slug:            id,
			Name:            id,
			OwnerSubjectKey: owner,
			Visibility:      models.VisibilityPersonal,
			DefaultAccess:   models.AccessModeReadWrite,
			Kind:            models.StorageKindKnowledge,
			Status:          models.StorageStatusActive,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
	}
	seedStorage("st-allowed", "user:oauth:alice")
	seedStorage("st-other", "user:oauth:bob")
	seedStorage("st-token-only", "user:oauth:carol")

	alice := "user:oauth:alice"
	if _, err := store.UpsertACL(context.Background(), models.ACLBinding{
		StorageID:  "st-allowed",
		SubjectKey: alice,
		Role:       models.RoleStorageReader,
	}); err != nil {
		t.Fatalf("UpsertACL: %v", err)
	}
	if _, err := store.UpsertACL(context.Background(), models.ACLBinding{
		StorageID:  "st-token-only",
		SubjectKey: alice,
		Role:       models.RoleStorageReader,
	}); err != nil {
		t.Fatalf("UpsertACL token storage: %v", err)
	}

	t.Run("acl subject returns bound storages only", func(t *testing.T) {
		got, err := store.ListAccessibleStorages(context.Background(), AccessibleStorageFilter{
			SubjectKeys: []string{alice, "all:authenticated"},
		})
		if err != nil {
			t.Fatalf("ListAccessibleStorages: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
	})

	t.Run("platform admin without token returns all storages", func(t *testing.T) {
		got, err := store.ListAccessibleStorages(context.Background(), AccessibleStorageFilter{
			SubjectKeys:   []string{alice},
			PlatformAdmin: true,
		})
		if err != nil {
			t.Fatalf("ListAccessibleStorages: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("len = %d, want 3", len(got))
		}
	})

	t.Run("token scopes intersect acl", func(t *testing.T) {
		got, err := store.ListAccessibleStorages(context.Background(), AccessibleStorageFilter{
			SubjectKeys:     []string{alice},
			HasToken:        true,
			TokenStorageIDs: []string{"st-token-only"},
		})
		if err != nil {
			t.Fatalf("ListAccessibleStorages: %v", err)
		}
		if len(got) != 1 || got[0].ID != "st-token-only" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("empty token scopes returns none", func(t *testing.T) {
		got, err := store.ListAccessibleStorages(context.Background(), AccessibleStorageFilter{
			SubjectKeys:     []string{alice},
			HasToken:        true,
			TokenStorageIDs: nil,
		})
		if err != nil {
			t.Fatalf("ListAccessibleStorages: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v", got)
		}
	})
}

func TestReadableStorageIDsUsesCandidateFilter(t *testing.T) {
	store := NewInMemoryStore()
	svc := NewService(store)
	now := time.Now().UTC()
	store.storages["st-1"] = models.Storage{
		ID: "st-1", Slug: "st-1", Name: "st-1", OwnerSubjectKey: "user:oauth:alice",
		Visibility: models.VisibilityPersonal, DefaultAccess: models.AccessModeReadWrite,
		Kind: models.StorageKindKnowledge, Status: models.StorageStatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
	store.storages["st-2"] = models.Storage{
		ID: "st-2", Slug: "st-2", Name: "st-2", OwnerSubjectKey: "user:oauth:bob",
		Visibility: models.VisibilityPersonal, DefaultAccess: models.AccessModeReadWrite,
		Kind: models.StorageKindKnowledge, Status: models.StorageStatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
	alice := models.Principal{UserID: "alice", SubjectKey: "user:oauth:alice", AuthSource: "oauth"}
	if _, err := store.UpsertACL(context.Background(), models.ACLBinding{
		StorageID: "st-1", SubjectKey: alice.SubjectKey, Role: models.RoleStorageReader,
	}); err != nil {
		t.Fatalf("UpsertACL: %v", err)
	}

	ids, err := svc.ReadableStorageIDs(context.Background(), alice, nil, nil)
	if err != nil {
		t.Fatalf("ReadableStorageIDs: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("len = %d, want 1", len(ids))
	}
	if _, ok := ids["st-1"]; !ok {
		t.Fatalf("missing st-1: %v", ids)
	}
}
