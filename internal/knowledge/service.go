package knowledge

import (
	"context"
	"errors"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/domainerr"
	"github.com/synamcps/synamcps-server/internal/knowledge/ingest"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/storage/meta"
	"github.com/synamcps/synamcps-server/internal/storage/vector"
)

type Service struct {
	catalog     meta.Catalog
	vectorStore vector.Store
	pipeline    *ingest.Pipeline
	access      *access.Service
	s3Bucket    string
}

func NewService(c meta.Catalog, v vector.Store, p *ingest.Pipeline, accessService *access.Service, s3Bucket string) (*Service, error) {
	if accessService == nil {
		return nil, errors.New("access service is required")
	}
	return &Service{
		catalog:     c,
		vectorStore: v,
		pipeline:    p,
		access:      accessService,
		s3Bucket:    s3Bucket,
	}, nil
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

func (s *Service) Save(ctx context.Context, p models.Principal, ac models.APIAccessContext, in SaveInput) (models.DocumentRecord, error) {
	if in.Visibility == "" {
		in.Visibility = models.VisibilityPersonal
	}
	if in.GroupIDs == nil {
		in.GroupIDs = []string{}
	}
	storageID := in.StorageID
	s3Prefix := ""
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
			return models.DocumentRecord{}, domainerr.ErrNotFound
		}
		s3Prefix = st.S3Prefix
	}
	if _, ok, err := s.access.CanAccessStorage(ctx, p, ac.AccessToken, ac.AllowedStorage, storageID, models.PermissionDocumentCreate); err != nil || !ok {
		if err != nil {
			return models.DocumentRecord{}, err
		}
		return models.DocumentRecord{}, domainerr.ErrForbidden
	}
	if !access.CanWriteVisibility(p, in.Visibility, in.GroupIDs) {
		return models.DocumentRecord{}, domainerr.ErrForbidden
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

func (s *Service) IngestBinary(ctx context.Context, p models.Principal, ac models.APIAccessContext, in BinaryInput) (models.DocumentRecord, error) {
	if in.Visibility == "" {
		in.Visibility = models.VisibilityPersonal
	}
	if in.GroupIDs == nil {
		in.GroupIDs = []string{}
	}
	storageID := in.StorageID
	s3Prefix := ""
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
			return models.DocumentRecord{}, domainerr.ErrNotFound
		}
		s3Prefix = st.S3Prefix
	}
	if _, ok, err := s.access.CanAccessStorage(ctx, p, ac.AccessToken, ac.AllowedStorage, storageID, models.PermissionDocumentCreate); err != nil || !ok {
		if err != nil {
			return models.DocumentRecord{}, err
		}
		return models.DocumentRecord{}, domainerr.ErrForbidden
	}
	if !access.CanWriteVisibility(p, in.Visibility, in.GroupIDs) {
		return models.DocumentRecord{}, domainerr.ErrForbidden
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

func (s *Service) Get(ctx context.Context, p models.Principal, ac models.APIAccessContext, id string) (models.DocumentRecord, error) {
	doc, ok, err := s.catalog.Get(ctx, id)
	if err != nil {
		return models.DocumentRecord{}, err
	}
	if !ok {
		return models.DocumentRecord{}, domainerr.ErrNotFound
	}
	if !s.canReadDoc(ctx, p, ac, doc) {
		return models.DocumentRecord{}, domainerr.ErrForbidden
	}
	return doc, nil
}

func (s *Service) List(ctx context.Context, p models.Principal, ac models.APIAccessContext, page models.PageRequest) (models.PaginatedKnowledgeList, error) {
	readable, err := s.access.ReadableStorageIDs(ctx, p, ac.AccessToken, ac.AllowedStorage)
	if err != nil {
		return models.PaginatedKnowledgeList{}, err
	}
	if page.StorageID != "" {
		if _, ok := readable[page.StorageID]; !ok {
			return models.PaginatedKnowledgeList{}, domainerr.ErrForbidden
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

func (s *Service) Delete(ctx context.Context, p models.Principal, ac models.APIAccessContext, id string) error {
	doc, ok, err := s.catalog.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return domainerr.ErrNotFound
	}
	if _, ok, err := s.access.CanAccessStorage(ctx, p, ac.AccessToken, ac.AllowedStorage, doc.StorageID, models.PermissionDocumentDelete); err != nil || !ok {
		if err != nil {
			return err
		}
		return domainerr.ErrForbidden
	}
	if !access.CanDeleteDocument(p, doc) {
		return domainerr.ErrForbidden
	}
	if err := s.vectorStore.DeleteByDocID(ctx, id); err != nil {
		return err
	}
	return s.catalog.Delete(ctx, id)
}

func (s *Service) Search(ctx context.Context, p models.Principal, ac models.APIAccessContext, req models.SearchRequest, allowPartial bool) ([]models.SearchHit, error) {
	page := req.Filters
	readable, err := s.access.ReadableStorageIDs(ctx, p, ac.AccessToken, ac.AllowedStorage)
	if err != nil {
		return nil, err
	}
	if page.StorageID != "" {
		if _, ok := readable[page.StorageID]; !ok {
			return nil, domainerr.ErrForbidden
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

func (s *Service) canReadDoc(ctx context.Context, p models.Principal, ac models.APIAccessContext, d models.DocumentRecord) bool {
	if _, ok, err := s.access.CanAccessStorage(ctx, p, ac.AccessToken, ac.AllowedStorage, d.StorageID, models.PermissionDocumentRead); err != nil || !ok {
		return false
	}
	return canSeeVisibility(p, d)
}

func (s *Service) canReadDocCached(p models.Principal, d models.DocumentRecord, readable map[string]struct{}) bool {
	if _, ok := readable[d.StorageID]; !ok {
		return false
	}
	return canSeeVisibility(p, d)
}

func (s *Service) embedQuery(ctx context.Context, query string) ([]float32, error) {
	if s.pipeline != nil {
		return s.pipeline.Embed(ctx, query)
	}
	return nil, errors.New("embedding pipeline not configured")
}

func canSeeVisibility(p models.Principal, d models.DocumentRecord) bool {
	switch d.Visibility {
	case models.VisibilityPublic:
		return true
	case models.VisibilityPersonal:
		return ownsDocumentKnowledge(p, d)
	case models.VisibilityGroup:
		return ownsDocumentKnowledge(p, d) || intersectsStrings(d.GroupIDs, p.Groups)
	default:
		return true
	}
}

func ownsDocumentKnowledge(p models.Principal, d models.DocumentRecord) bool {
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

func extractSnippet(text, query string) string {
	if query == "" {
		return text
	}
	runes := []rune(text)
	lower := make([]rune, len(runes))
	qRunes := []rune(query)
	for i, r := range runes {
		lower[i] = r
		if r >= 'A' && r <= 'Z' {
			lower[i] = r + ('a' - 'A')
		}
	}
	qLower := make([]rune, len(qRunes))
	for i, r := range qRunes {
		qLower[i] = r
		if r >= 'A' && r <= 'Z' {
			qLower[i] = r + ('a' - 'A')
		}
	}
	idx := -1
	for i := 0; i+len(qLower) <= len(lower); i++ {
		match := true
		for j := range qLower {
			if lower[i+j] != qLower[j] {
				match = false
				break
			}
		}
		if match {
			idx = i
			break
		}
	}
	if idx < 0 {
		if len(runes) > 180 {
			return string(runes[:180])
		}
		return text
	}
	start := idx - 50
	if start < 0 {
		start = 0
	}
	end := idx + len(qRunes) + 100
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[start:end])
}
