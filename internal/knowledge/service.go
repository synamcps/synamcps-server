package knowledge

import (
	"context"
	"errors"
	"strings"

	"github.com/zmiishe/synamcps/internal/access"
	"github.com/zmiishe/synamcps/internal/auth"
	"github.com/zmiishe/synamcps/internal/knowledge/ingest"
	"github.com/zmiishe/synamcps/internal/models"
	"github.com/zmiishe/synamcps/internal/policy"
	"github.com/zmiishe/synamcps/internal/storage/meta"
	"github.com/zmiishe/synamcps/internal/storage/vector"
)

type Service struct {
	catalog     meta.Catalog
	vectorStore vector.Store
	pipeline    *ingest.Pipeline
	access      *access.Service
	s3Bucket    string
}

func NewService(c meta.Catalog, v vector.Store, p *ingest.Pipeline) *Service {
	return &Service{catalog: c, vectorStore: v, pipeline: p}
}

func (s *Service) AttachAccess(accessService *access.Service, s3Bucket string) {
	s.access = accessService
	s.s3Bucket = s3Bucket
}

type SaveInput struct {
	StorageID  string            `json:"storageId"`
	Title      string            `json:"title"`
	Text       string            `json:"text"`
	MimeType   string            `json:"mimeType"`
	Visibility models.Visibility `json:"visibility"`
	GroupIDs   []string          `json:"groupIds"`
	Source     string            `json:"source,omitempty"`
	SourceURL  string            `json:"sourceUrl,omitempty"`
	Channel    string            `json:"-"`
}

func (s *Service) Save(ctx context.Context, p models.Principal, in SaveInput) (models.DocumentRecord, error) {
	if in.Visibility == "" {
		in.Visibility = models.VisibilityPersonal
	}
	if in.GroupIDs == nil {
		in.GroupIDs = []string{}
	}
	storageID := in.StorageID
	s3Prefix := ""
	if s.access != nil {
		if storageID == "" {
			_, st, err := s.access.EnsurePrincipal(ctx, p, s.s3Bucket)
			if err != nil {
				return models.DocumentRecord{}, err
			}
			storageID = st.ID
			s3Prefix = st.S3Prefix
		} else {
			st, ok, err := s.access.Store().GetStorage(ctx, storageID)
			if err != nil {
				return models.DocumentRecord{}, err
			}
			if !ok {
				return models.DocumentRecord{}, errors.New("storage not found")
			}
			s3Prefix = st.S3Prefix
		}
		if _, ok, err := s.access.CanAccessStorage(ctx, p, accessTokenFromContext(ctx), tokenScopesFromContext(ctx), storageID, models.PermissionDocumentCreate); err != nil || !ok {
			if err != nil {
				return models.DocumentRecord{}, err
			}
			return models.DocumentRecord{}, errors.New("forbidden")
		}
	} else if !policy.CanWrite(p, in.Visibility, in.GroupIDs) {
		return models.DocumentRecord{}, errors.New("forbidden")
	}
	return s.pipeline.Save(ctx, ingest.SaveRequest{
		Principal:  p,
		StorageID:  storageID,
		S3Prefix:   s3Prefix,
		Title:      in.Title,
		Body:       in.Text,
		MimeType:   in.MimeType,
		Visibility: in.Visibility,
		GroupIDs:   in.GroupIDs,
		Source:     in.Source,
		SourceURL:  in.SourceURL,
		Channel:    in.Channel,
	})
}

type BinaryInput struct {
	StorageID  string            `json:"storageId"`
	Title      string            `json:"title"`
	Filename   string            `json:"filename"`
	Payload    []byte            `json:"-"`
	MimeType   string            `json:"mimeType"`
	Visibility models.Visibility `json:"visibility"`
	GroupIDs   []string          `json:"groupIds"`
	Source     string            `json:"source,omitempty"`
	SourceURL  string            `json:"sourceUrl,omitempty"`
	Channel    string            `json:"-"`
}

func (s *Service) IngestBinary(ctx context.Context, p models.Principal, in BinaryInput) (models.DocumentRecord, error) {
	if in.Visibility == "" {
		in.Visibility = models.VisibilityPersonal
	}
	if in.GroupIDs == nil {
		in.GroupIDs = []string{}
	}
	storageID := in.StorageID
	s3Prefix := ""
	if s.access != nil {
		if storageID == "" {
			_, st, err := s.access.EnsurePrincipal(ctx, p, s.s3Bucket)
			if err != nil {
				return models.DocumentRecord{}, err
			}
			storageID = st.ID
			s3Prefix = st.S3Prefix
		} else {
			st, ok, err := s.access.Store().GetStorage(ctx, storageID)
			if err != nil {
				return models.DocumentRecord{}, err
			}
			if !ok {
				return models.DocumentRecord{}, errors.New("storage not found")
			}
			s3Prefix = st.S3Prefix
		}
		if _, ok, err := s.access.CanAccessStorage(ctx, p, accessTokenFromContext(ctx), tokenScopesFromContext(ctx), storageID, models.PermissionDocumentCreate); err != nil || !ok {
			if err != nil {
				return models.DocumentRecord{}, err
			}
			return models.DocumentRecord{}, errors.New("forbidden")
		}
	} else if !policy.CanWrite(p, in.Visibility, in.GroupIDs) {
		return models.DocumentRecord{}, errors.New("forbidden")
	}

	return s.pipeline.SaveBinary(ctx, ingest.BinarySaveRequest{
		Principal:  p,
		StorageID:  storageID,
		S3Prefix:   s3Prefix,
		Title:      in.Title,
		Filename:   in.Filename,
		Payload:    in.Payload,
		MimeType:   in.MimeType,
		Visibility: in.Visibility,
		GroupIDs:   in.GroupIDs,
		Source:     in.Source,
		SourceURL:  in.SourceURL,
		Channel:    in.Channel,
	})
}

func (s *Service) Get(ctx context.Context, p models.Principal, id string) (models.DocumentRecord, error) {
	doc, ok, err := s.catalog.Get(ctx, id)
	if err != nil {
		return models.DocumentRecord{}, err
	}
	if !ok {
		return models.DocumentRecord{}, errors.New("not found")
	}
	if !s.canReadDoc(ctx, p, doc) {
		return models.DocumentRecord{}, errors.New("forbidden")
	}
	return doc, nil
}

func (s *Service) List(ctx context.Context, p models.Principal, page models.PageRequest) (models.PaginatedKnowledgeList, error) {
	if s.access != nil {
		// Resolve the readable storage set once, then let the catalog apply
		// authorization + visibility inside SQL. This keeps pagination counts
		// correct and avoids a permission query per returned row.
		readable, err := s.access.ReadableStorageIDs(ctx, p, accessTokenFromContext(ctx), tokenScopesFromContext(ctx))
		if err != nil {
			return models.PaginatedKnowledgeList{}, err
		}
		if page.StorageID != "" {
			if _, ok := readable[page.StorageID]; !ok {
				return models.PaginatedKnowledgeList{}, errors.New("forbidden")
			}
			page.AllowedStorageIDs = []string{page.StorageID}
		} else {
			if len(readable) == 0 {
				return emptyPage(page), nil
			}
			ids := make([]string, 0, len(readable))
			for id := range readable {
				ids = append(ids, id)
			}
			page.AllowedStorageIDs = ids
		}
		page.ApplyVisibility = true
		page.VisibilityOwnerIDs = ownerIdentifiers(p)
		page.VisibilityGroups = p.Groups
		return s.catalog.List(ctx, page)
	}

	// Legacy path (no access service): policy-based post-filter.
	all, err := s.catalog.List(ctx, page)
	if err != nil {
		return models.PaginatedKnowledgeList{}, err
	}
	filtered := make([]models.DocumentRecord, 0, len(all.Items))
	for _, d := range all.Items {
		if policy.CanRead(p, d) {
			filtered = append(filtered, d)
		}
	}
	all.Items = filtered
	all.Total = int64(len(filtered))
	all.HasNext = int64(all.Page*all.PageSize) < all.Total
	return all, nil
}

func emptyPage(page models.PageRequest) models.PaginatedKnowledgeList {
	ps := page.PageSize
	if ps <= 0 {
		ps = 20
	}
	pg := page.Page
	if pg <= 0 {
		pg = 1
	}
	return models.PaginatedKnowledgeList{Items: []models.DocumentRecord{}, Page: pg, PageSize: ps}
}

func ownerIdentifiers(p models.Principal) []string {
	out := make([]string, 0, 2)
	seen := map[string]struct{}{}
	for _, id := range []string{p.UserID, models.SubjectKeyForPrincipal(p)} {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s *Service) Delete(ctx context.Context, p models.Principal, id string) error {
	doc, ok, err := s.catalog.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not found")
	}
	if s.access != nil {
		if _, ok, err := s.access.CanAccessStorage(ctx, p, accessTokenFromContext(ctx), tokenScopesFromContext(ctx), doc.StorageID, models.PermissionDocumentDelete); err != nil || !ok {
			if err != nil {
				return err
			}
			return errors.New("forbidden")
		}
	} else if !policy.CanDelete(p, doc) {
		return errors.New("forbidden")
	}
	if err := s.vectorStore.DeleteByDocID(ctx, id); err != nil {
		return err
	}
	return s.catalog.Delete(ctx, id)
}

func (s *Service) Search(ctx context.Context, p models.Principal, req models.SearchRequest, allowPartial bool) ([]models.SearchHit, error) {
	page := req.Filters
	var readable map[string]struct{}
	if s.access != nil {
		var err error
		readable, err = s.access.ReadableStorageIDs(ctx, p, accessTokenFromContext(ctx), tokenScopesFromContext(ctx))
		if err != nil {
			return nil, err
		}
		if page.StorageID != "" {
			if _, ok := readable[page.StorageID]; !ok {
				return nil, errors.New("forbidden")
			}
		}
	}

	if page.SourceURLMode == "" {
		page.SourceURLMode = "exact"
	}
	if page.SourceURLMode == "partial" && !allowPartial {
		page.SourceURLMode = "exact"
	}

	queryVec, err := s.embedQuery(ctx, req.Query)
	if err != nil {
		return nil, err
	}
	recs, err := s.vectorStore.Search(ctx, queryVec, req.TopK, page)
	if err != nil {
		return nil, err
	}

	hits := make([]models.SearchHit, 0, len(recs))
	for _, r := range recs {
		doc, ok, err := s.catalog.Get(ctx, r.Payload.DocID)
		if err != nil || !ok {
			continue
		}
		if !s.canReadDocCached(p, doc, readable) {
			continue
		}
		snippet := r.Text
		if req.Query != "" {
			snippet = extractSnippet(r.Text, req.Query)
		}
		hits = append(hits, models.SearchHit{
			DocID:      doc.DocID,
			Title:      doc.Title,
			Snippet:    snippet,
			Score:      1.0,
			Visibility: doc.Visibility,
			Source:     doc.Source,
			SourceURL:  doc.SourceURL,
		})
	}
	return hits, nil
}

func (s *Service) canReadDoc(ctx context.Context, p models.Principal, d models.DocumentRecord) bool {
	if s.access == nil {
		return policy.CanRead(p, d)
	}
	if _, ok, err := s.access.CanAccessStorage(ctx, p, accessTokenFromContext(ctx), tokenScopesFromContext(ctx), d.StorageID, models.PermissionDocumentRead); err != nil || !ok {
		return false
	}
	// Storage access is necessary but not sufficient: a "personal" document must
	// only be visible to its owner, even to others who can read the storage.
	return canSeeVisibility(p, d)
}

// canReadDocCached is canReadDoc using a pre-computed readable-storage set,
// avoiding a per-document permission query in hot loops (e.g. search results).
func (s *Service) canReadDocCached(p models.Principal, d models.DocumentRecord, readable map[string]struct{}) bool {
	if s.access == nil {
		return policy.CanRead(p, d)
	}
	if _, ok := readable[d.StorageID]; !ok {
		return false
	}
	return canSeeVisibility(p, d)
}

func (s *Service) embedQuery(ctx context.Context, query string) ([]float32, error) {
	if s.pipeline != nil {
		return s.pipeline.Embed(ctx, query)
	}
	return []float32{0.1, 0.2}, nil
}

// canSeeVisibility enforces per-document visibility on top of storage access.
// Empty/unknown visibility falls back to storage-level access (allow) to avoid
// hiding legacy records that predate the visibility field.
func canSeeVisibility(p models.Principal, d models.DocumentRecord) bool {
	switch d.Visibility {
	case models.VisibilityPublic:
		return true
	case models.VisibilityPersonal:
		return ownsDocument(p, d)
	case models.VisibilityGroup:
		return ownsDocument(p, d) || intersectsStrings(d.GroupIDs, p.Groups)
	default:
		return true
	}
}

func ownsDocument(p models.Principal, d models.DocumentRecord) bool {
	if d.OwnerID == "" {
		return false
	}
	return d.OwnerID == p.UserID || d.OwnerID == models.SubjectKeyForPrincipal(p)
}

func intersectsStrings(a, b []string) bool {
	set := make(map[string]struct{}, len(a))
	for _, v := range a {
		set[v] = struct{}{}
	}
	for _, v := range b {
		if _, ok := set[v]; ok {
			return true
		}
	}
	return false
}

func accessTokenFromContext(ctx context.Context) *models.AccessToken {
	ac, ok := auth.AccessContextFromContext(ctx)
	if !ok {
		return nil
	}
	return ac.AccessToken
}

func tokenScopesFromContext(ctx context.Context) []models.AccessTokenStorage {
	ac, ok := auth.AccessContextFromContext(ctx)
	if !ok {
		return nil
	}
	return ac.AllowedStorage
}

func extractSnippet(text, query string) string {
	if query == "" {
		return text
	}
	lower := strings.ToLower(text)
	q := strings.ToLower(query)
	idx := strings.Index(lower, q)
	if idx < 0 {
		if len(text) > 180 {
			return text[:180]
		}
		return text
	}
	start := idx - 50
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + 100
	if end > len(text) {
		end = len(text)
	}
	return text[start:end]
}
