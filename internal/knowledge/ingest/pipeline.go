package ingest

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zmiishe/synamcps/internal/config"
	"github.com/zmiishe/synamcps/internal/llm"
	"github.com/zmiishe/synamcps/internal/models"
	"github.com/zmiishe/synamcps/internal/storage/blob"
	"github.com/zmiishe/synamcps/internal/storage/meta"
	"github.com/zmiishe/synamcps/internal/storage/vector"
)

type Pipeline struct {
	cfg         config.Config
	summarizer  llm.Summarizer
	embedder    llm.EmbeddingProvider
	vectorStore vector.Store
	catalog     meta.Catalog
	blobStore   *blob.Store
}

func NewPipeline(
	cfg config.Config,
	summarizer llm.Summarizer,
	embedder llm.EmbeddingProvider,
	vectorStore vector.Store,
	catalog meta.Catalog,
	blobStore *blob.Store,
) *Pipeline {
	return &Pipeline{
		cfg:         cfg,
		summarizer:  summarizer,
		embedder:    embedder,
		vectorStore: vectorStore,
		catalog:     catalog,
		blobStore:   blobStore,
	}
}

type SaveRequest struct {
	Principal  models.Principal
	StorageID  string
	S3Prefix   string
	RawS3Key   string
	Title      string
	Body       string
	MimeType   string
	Visibility models.Visibility
	GroupIDs   []string
	Source     string
	SourceURL  string
	Channel    string
}

func (p *Pipeline) Save(ctx context.Context, req SaveRequest) (models.DocumentRecord, error) {
	source := normalizeSource(req.Source, req.Channel)
	if req.Visibility == "" {
		req.Visibility = models.VisibilityPersonal
	}
	if req.GroupIDs == nil {
		req.GroupIDs = []string{}
	}
	if req.SourceURL != "" {
		if _, err := url.ParseRequestURI(req.SourceURL); err != nil {
			return models.DocumentRecord{}, fmt.Errorf("invalid sourceUrl: %w", err)
		}
	}

	doc := models.DocumentRecord{
		DocID:      uuid.NewString(),
		StorageID:  defaultStorageID(req.StorageID),
		OwnerID:    req.Principal.UserID,
		Visibility: req.Visibility,
		GroupIDs:   req.GroupIDs,
		Title:      req.Title,
		MimeType:   req.MimeType,
		Source:     source,
		SourceURL:  req.SourceURL,
		Status:     "processing",
		Body:       req.Body,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}

	if req.RawS3Key != "" {
		doc.S3Key = req.RawS3Key
	}

	if int64(len(req.Body)) > p.cfg.S3.LargeDocBytes && p.cfg.S3.LargeDocBytes > 0 {
		prefix := req.S3Prefix
		if prefix == "" {
			prefix = "storages/" + doc.StorageID + "/"
		}
		doc.S3Key = strings.TrimRight(prefix, "/") + "/documents/" + doc.DocID + "/source.txt"
		if err := p.blobStore.Put(ctx, doc.S3Key, []byte(req.Body)); err != nil {
			return models.DocumentRecord{}, err
		}
	}

	summaryText, summaryModel, err := p.summarizer.Summarize(ctx, req.Body)
	if err != nil {
		return models.DocumentRecord{}, err
	}
	chunks := splitText(req.Body, p.cfg.Chunking.ChunkSize, p.cfg.Chunking.Overlap)
	if len(chunks) == 0 {
		chunks = []string{req.Body}
	}

	summaryChunkID := uuid.NewString()
	doc.SummaryChunkID = summaryChunkID
	summaryEmbedding, embeddingModel, err := p.embedder.Embed(ctx, summaryText)
	if err != nil {
		return models.DocumentRecord{}, err
	}
	if err := p.vectorStore.Upsert(ctx, vector.Record{
		Vector: summaryEmbedding,
		Text:   summaryText,
		Payload: models.VectorPayload{
			DocID:      doc.DocID,
			StorageID:  doc.StorageID,
			ChunkID:    summaryChunkID,
			Visibility: doc.Visibility,
			OwnerID:    doc.OwnerID,
			GroupIDs:   doc.GroupIDs,
			IsSummary:  true,
			Source:     doc.Source,
			SourceURL:  doc.SourceURL,
			S3Key:      doc.S3Key,
		},
	}); err != nil {
		return models.DocumentRecord{}, err
	}

	for i, chunk := range chunks {
		vec, _, err := p.embedder.Embed(ctx, chunk)
		if err != nil {
			return models.DocumentRecord{}, err
		}
		if err := p.vectorStore.Upsert(ctx, vector.Record{
			Vector: vec,
			Text:   chunk,
			Payload: models.VectorPayload{
				DocID:      doc.DocID,
				StorageID:  doc.StorageID,
				ChunkID:    fmt.Sprintf("%s-%d", doc.DocID, i),
				Visibility: doc.Visibility,
				OwnerID:    doc.OwnerID,
				GroupIDs:   doc.GroupIDs,
				IsSummary:  false,
				Source:     doc.Source,
				SourceURL:  doc.SourceURL,
				S3Key:      doc.S3Key,
			},
		}); err != nil {
			return models.DocumentRecord{}, err
		}
	}

	doc.Status = "ready"
	doc.SourceHash = fmt.Sprintf("%s:%s", summaryModel, embeddingModel)
	if err := p.catalog.Save(ctx, doc); err != nil {
		return models.DocumentRecord{}, err
	}
	return doc, nil
}

type BinarySaveRequest struct {
	Principal  models.Principal
	StorageID  string
	S3Prefix   string
	Title      string
	Filename   string
	Payload    []byte
	MimeType   string
	Visibility models.Visibility
	GroupIDs   []string
	Source     string
	SourceURL  string
	Channel    string
}

func (p *Pipeline) SaveBinary(ctx context.Context, req BinarySaveRequest) (models.DocumentRecord, error) {
	if req.Visibility == "" {
		req.Visibility = models.VisibilityPersonal
	}
	if req.GroupIDs == nil {
		req.GroupIDs = []string{}
	}

	storageID := defaultStorageID(req.StorageID)
	prefix := req.S3Prefix
	if prefix == "" {
		prefix = "storages/" + storageID + "/"
	}
	fileName := strings.TrimSpace(req.Filename)
	if fileName == "" {
		fileName = "upload.bin"
	}
	rawKey := strings.TrimRight(prefix, "/") + "/items/" + uuid.NewString() + "/" + sanitizeS3Name(fileName)
	if err := p.blobStore.Put(ctx, rawKey, req.Payload); err != nil {
		return models.DocumentRecord{}, err
	}

	text := extractTextBestEffort(req.Payload, req.MimeType)
	if strings.TrimSpace(req.Title) == "" {
		req.Title = fileName
	}
	mime := req.MimeType
	if mime == "" {
		mime = "application/octet-stream"
	}
	return p.Save(ctx, SaveRequest{
		Principal:  req.Principal,
		StorageID:  storageID,
		S3Prefix:   prefix,
		RawS3Key:   rawKey,
		Title:      req.Title,
		Body:       text,
		MimeType:   mime,
		Visibility: req.Visibility,
		GroupIDs:   req.GroupIDs,
		Source:     req.Source,
		SourceURL:  req.SourceURL,
		Channel:    req.Channel,
	})
}

func sanitizeS3Name(name string) string {
	// keep it simple and predictable
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "file.bin"
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.' || r == '-' || r == '_' || r == ' ':
			return r
		default:
			return '-'
		}
	}, name)
	return strings.ReplaceAll(name, " ", "-")
}

func extractTextBestEffort(payload []byte, mime string) string {
	if len(payload) == 0 {
		return ""
	}
	m := strings.ToLower(strings.TrimSpace(mime))
	if strings.HasPrefix(m, "text/") || m == "application/json" || m == "application/xml" || m == "application/xhtml+xml" {
		return string(payload)
	}
	// best-effort UTF-8 decode and strip obvious binary noise
	s := string(payload)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return r
		}
		if r < 32 {
			return -1
		}
		return r
	}, s)
	if len(strings.TrimSpace(s)) == 0 {
		return ""
	}
	return s
}

func defaultStorageID(id string) string {
	if id == "" {
		return "legacy"
	}
	return id
}

func splitText(text string, chunkSize, overlap int) []string {
	if chunkSize <= 0 {
		chunkSize = 500
	}
	if overlap < 0 {
		overlap = 0
	}

	tokens := strings.Fields(text)
	if len(tokens) == 0 {
		return nil
	}

	var out []string
	step := chunkSize - overlap
	if step <= 0 {
		step = chunkSize
	}
	for i := 0; i < len(tokens); i += step {
		end := i + chunkSize
		if end > len(tokens) {
			end = len(tokens)
		}
		out = append(out, strings.Join(tokens[i:end], " "))
		if end == len(tokens) {
			break
		}
	}
	return out
}

func normalizeSource(source, channel string) string {
	if source != "" {
		return source
	}
	switch channel {
	case "mcp":
		return "mcp"
	case "api":
		return "api"
	case "admin":
		return "admin"
	default:
		return "unknown"
	}
}
