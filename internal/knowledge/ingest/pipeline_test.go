package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/synamcps/synamcps-server/internal/config"
	"github.com/synamcps/synamcps-server/internal/llm"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/storage/blob"
	metapg "github.com/synamcps/synamcps-server/internal/storage/meta/postgres"
	"github.com/synamcps/synamcps-server/internal/storage/vector/pgvector"
)

func testPipeline(t *testing.T) (*Pipeline, *Worker, *metapg.Store, *pgvector.Store) {
	t.Helper()
	cfg := config.Config{
		Chunking:      config.ChunkingConfig{ChunkSize: 10, Overlap: 2},
		S3:            config.S3Config{LargeDocBytes: 1000000},
		Embedding:     config.ModelConfig{Model: "emb"},
		Summarization: config.ModelConfig{Model: "sum", MaxOutputTokens: 10},
	}
	catalog := metapg.NewInMemory()
	vec := pgvector.NewInMemory()
	blobStore, err := blob.NewStore(config.Config{})
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	jobs := NewInMemoryJobStore()
	p := NewPipeline(PipelineConfig{
		Chunking:      cfg.Chunking,
		LargeDocBytes: cfg.S3.LargeDocBytes,
	}, llm.NewSimpleSummarizer(cfg.Summarization), llm.NewSimpleEmbeddingProvider(cfg.Embedding), vec, catalog, blobStore, jobs)
	w := NewWorker(p, jobs, WorkerConfig{})
	return p, w, catalog, vec
}

func TestSaveReturnsProcessingThenWorkerIndexes(t *testing.T) {
	ctx := context.Background()
	p, w, catalog, vec := testPipeline(t)

	principal := models.Principal{UserID: "alice", SubjectKey: "user:oauth:alice"}
	doc, err := p.Save(ctx, SaveRequest{
		Principal:  principal,
		StorageID:  "legacy",
		Title:      "Hello",
		Body:       "hello world from async ingest",
		MimeType:   "text/plain",
		Visibility: models.VisibilityPersonal,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if doc.Status != models.DocumentStatusProcessing {
		t.Fatalf("status = %q, want processing", doc.Status)
	}

	stored, ok, err := catalog.Get(ctx, doc.DocID)
	if err != nil || !ok {
		t.Fatalf("catalog get: ok=%v err=%v", ok, err)
	}
	if stored.Status != models.DocumentStatusProcessing {
		t.Fatalf("stored status = %q", stored.Status)
	}

	if err := w.ProcessUntilIdle(ctx); err != nil {
		t.Fatalf("ProcessUntilIdle: %v", err)
	}

	stored, ok, err = catalog.Get(ctx, doc.DocID)
	if err != nil || !ok {
		t.Fatalf("catalog get after worker: ok=%v err=%v", ok, err)
	}
	if stored.Status != models.DocumentStatusReady {
		t.Fatalf("status after worker = %q, want ready", stored.Status)
	}
	if stored.SummaryChunkID == "" {
		t.Fatal("summary chunk id not set")
	}

	hits, err := vec.Search(ctx, []float32{0.1, 0.2}, 5, models.PageRequest{})
	if err != nil {
		t.Fatalf("vector search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected indexed vectors")
	}
}

func TestDeleteEnqueuesVectorCleanup(t *testing.T) {
	ctx := context.Background()
	p, w, catalog, vec := testPipeline(t)

	principal := models.Principal{UserID: "bob", SubjectKey: "user:oauth:bob"}
	doc, err := p.Save(ctx, SaveRequest{
		Principal:  principal,
		Title:      "To delete",
		Body:       "delete me please",
		MimeType:   "text/plain",
		Visibility: models.VisibilityPersonal,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := w.ProcessUntilIdle(ctx); err != nil {
		t.Fatalf("ProcessUntilIdle: %v", err)
	}

	doc.Status = models.DocumentStatusDeleting
	doc.UpdatedAt = time.Now().UTC()
	if err := catalog.Save(ctx, doc); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := catalog.Delete(ctx, doc.DocID); err != nil {
		t.Fatalf("catalog delete: %v", err)
	}
	if err := w.EnqueueDelete(ctx, doc.DocID); err != nil {
		t.Fatalf("EnqueueDelete: %v", err)
	}
	if err := w.ProcessUntilIdle(ctx); err != nil {
		t.Fatalf("ProcessUntilIdle delete: %v", err)
	}

	hits, err := vec.Search(ctx, []float32{0.1, 0.2}, 5, models.PageRequest{})
	if err != nil {
		t.Fatalf("vector search: %v", err)
	}
	for _, hit := range hits {
		if hit.Payload.DocID == doc.DocID {
			t.Fatalf("vectors still present for deleted doc")
		}
	}
}

func TestIngestFailureMarksDocumentFailed(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{
		Chunking:      config.ChunkingConfig{ChunkSize: 10, Overlap: 2},
		S3:            config.S3Config{LargeDocBytes: 1000000},
		Embedding:     config.ModelConfig{Model: "emb"},
		Summarization: config.ModelConfig{Model: "sum", MaxOutputTokens: 10},
	}
	catalog := metapg.NewInMemory()
	vec := pgvector.NewInMemory()
	blobStore, err := blob.NewStore(config.Config{})
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	jobs := NewInMemoryJobStore()
	p := NewPipeline(PipelineConfig{
		Chunking:      cfg.Chunking,
		LargeDocBytes: cfg.S3.LargeDocBytes,
	}, &failSummarizer{}, llm.NewSimpleEmbeddingProvider(cfg.Embedding), vec, catalog, blobStore, jobs)
	w := NewWorker(p, jobs, WorkerConfig{MaxAttempts: 1})

	doc, err := p.Save(ctx, SaveRequest{
		Principal:  models.Principal{UserID: "carol"},
		Title:      "Fail",
		Body:       "will fail",
		MimeType:   "text/plain",
		Visibility: models.VisibilityPersonal,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := w.ProcessUntilIdle(ctx); err != nil {
		t.Fatalf("ProcessUntilIdle: %v", err)
	}
	stored, ok, err := catalog.Get(ctx, doc.DocID)
	if err != nil || !ok {
		t.Fatalf("catalog get: ok=%v err=%v", ok, err)
	}
	if stored.Status != models.DocumentStatusFailed {
		t.Fatalf("status = %q, want failed", stored.Status)
	}
}

type failSummarizer struct{}

func (failSummarizer) Summarize(context.Context, string) (string, string, error) {
	return "", "", errSummarize
}

var errSummarize = &summarizeError{}

type summarizeError struct{}

func (summarizeError) Error() string { return "summarize failed" }
