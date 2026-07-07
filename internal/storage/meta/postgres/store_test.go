package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/synamcps/synamcps-server/internal/models"
)

func TestGetMany(t *testing.T) {
	ctx := context.Background()
	store := NewInMemory()
	now := time.Now().UTC()
	docs := []models.DocumentRecord{
		{DocID: "doc-1", Title: "One", Status: "ready", CreatedAt: now, UpdatedAt: now},
		{DocID: "doc-2", Title: "Two", Status: "ready", CreatedAt: now, UpdatedAt: now},
	}
	for _, doc := range docs {
		if err := store.Save(ctx, doc); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	t.Run("empty ids", func(t *testing.T) {
		got, err := store.GetMany(ctx, nil)
		if err != nil {
			t.Fatalf("GetMany: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("len = %d, want 0", len(got))
		}
	})

	t.Run("returns only existing docs", func(t *testing.T) {
		got, err := store.GetMany(ctx, []string{"doc-1", "doc-missing", "doc-2"})
		if err != nil {
			t.Fatalf("GetMany: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got["doc-1"].Title != "One" || got["doc-2"].Title != "Two" {
			t.Fatalf("got %+v", got)
		}
	})
}
