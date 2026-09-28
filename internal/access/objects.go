package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/synamcps/synamcps-server/internal/dataobj"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/strutil"
)

func (s *Store) CreateObject(ctx context.Context, obj models.DataObject, actor string) (models.DataObject, error) {
	now := time.Now().UTC()
	if obj.ID == "" {
		obj.ID = uuid.NewString()
	}
	if obj.Slug == "" {
		obj.Slug = obj.ID
	}
	if obj.Name == "" {
		obj.Name = obj.Slug
	}
	if obj.Status == "" {
		obj.Status = models.StorageStatusActive
	}
	if obj.Visibility == "" {
		obj.Visibility = models.VisibilityPersonal
	}
	if obj.DefaultAccess == "" {
		obj.DefaultAccess = models.AccessModeNone
	}
	if obj.Backend == "" {
		obj.Backend = "postgres"
	}
	if obj.Config == nil {
		obj.Config = map[string]any{}
	}
	if obj.S3Prefix == "" {
		obj.S3Prefix = "objects/" + obj.ID + "/"
	}
	obj.CreatedAt = now
	obj.UpdatedAt = now
	if !dataobj.ValidKind(obj.Kind) {
		return models.DataObject{}, fmt.Errorf("invalid object kind %q", obj.Kind)
	}

	cfgRaw, _ := json.Marshal(obj.Config)
	binding := models.ObjectACLBinding{
		ID:         uuid.NewString(),
		ObjectID:   obj.ID,
		SubjectKey: obj.OwnerSubjectKey,
		Role:       models.RoleStorageOwner,
		GrantedBy:  actor,
		CreatedAt:  now,
	}

	if !s.useDB {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.objects == nil {
			s.objects = map[string]models.DataObject{}
		}
		if s.objectACL == nil {
			s.objectACL = map[string]models.ObjectACLBinding{}
		}
		s.objects[obj.ID] = obj
		s.objectACL[binding.ID] = binding
		if obj.Kind == models.ObjectKindStorage {
			s.storages[obj.ID] = storageFromObject(obj)
			s.acl[binding.ID] = models.ACLBinding{
				ID: binding.ID, StorageID: obj.ID, SubjectKey: binding.SubjectKey,
				Role: binding.Role, GrantedBy: binding.GrantedBy, CreatedAt: binding.CreatedAt,
			}
		}
		return obj, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.DataObject{}, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `INSERT INTO data_objects
(id, kind, slug, name, owner_subject_key, visibility, default_access, status, backend, config_json, s3_bucket, s3_prefix, created_at, updated_at, archived_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,$13,$14,$15)`,
		obj.ID, string(obj.Kind), obj.Slug, obj.Name, obj.OwnerSubjectKey, string(obj.Visibility), string(obj.DefaultAccess),
		string(obj.Status), obj.Backend, string(cfgRaw), obj.S3Bucket, obj.S3Prefix, obj.CreatedAt, obj.UpdatedAt, obj.ArchivedAt)
	if err != nil {
		return models.DataObject{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO object_acl_bindings (id, object_id, subject_key, role, granted_by, expires_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (object_id, subject_key, role) DO NOTHING`,
		binding.ID, binding.ObjectID, binding.SubjectKey, string(binding.Role), binding.GrantedBy, binding.ExpiresAt, binding.CreatedAt)
	if err != nil {
		return models.DataObject{}, err
	}
	if obj.Kind == models.ObjectKindStorage {
		st := storageFromObject(obj)
		_, err = tx.Exec(ctx, `INSERT INTO storages (id, slug, name, owner_subject_key, visibility, default_access, storage_kind, s3_bucket, s3_prefix, status, created_at, updated_at, archived_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT (id) DO NOTHING`,
			st.ID, st.Slug, st.Name, st.OwnerSubjectKey, string(st.Visibility), string(st.DefaultAccess), string(st.Kind),
			st.S3Bucket, st.S3Prefix, string(st.Status), st.CreatedAt, st.UpdatedAt, st.ArchivedAt)
		if err != nil {
			return models.DataObject{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO storage_acl_bindings (id, storage_id, subject_key, role, granted_by, expires_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (storage_id, subject_key, role) DO NOTHING`,
			binding.ID, obj.ID, binding.SubjectKey, string(binding.Role), binding.GrantedBy, binding.ExpiresAt, binding.CreatedAt)
		if err != nil {
			return models.DataObject{}, err
		}
	}
	if err := provisionObjectBackend(ctx, tx, obj); err != nil {
		return models.DataObject{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.DataObject{}, err
	}
	_ = s.EnsureUserRole(ctx, obj.OwnerSubjectKey)
	return obj, nil
}

func provisionObjectBackend(ctx context.Context, tx pgx.Tx, obj models.DataObject) error {
	switch obj.Kind {
	case models.ObjectKindCounter:
		_, err := tx.Exec(ctx, `INSERT INTO counter_meta (object_id, value, updated_at) VALUES ($1,0,$2) ON CONFLICT DO NOTHING`, obj.ID, time.Now().UTC())
		return err
	case models.ObjectKindTable:
		schema := tableSchemaName(obj.ID)
		table := "data"
		if !safeIdent(schema) || !safeIdent(table) {
			return errors.New("invalid table identifiers")
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, schema)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s (id BIGSERIAL PRIMARY KEY, payload JSONB NOT NULL DEFAULT '{}'::jsonb, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`, schema, table)); err != nil {
			return err
		}
		ownerRole := roleNameForSubject(obj.OwnerSubjectKey)
		if safeRoleName(ownerRole) {
			_, _ = tx.Exec(ctx, fmt.Sprintf(`DO $$ BEGIN CREATE ROLE %s NOLOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END $$`, ownerRole))
			_, _ = tx.Exec(ctx, fmt.Sprintf(`GRANT USAGE ON SCHEMA %s TO %s`, schema, ownerRole))
			_, _ = tx.Exec(ctx, fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON %s.%s TO %s`, schema, table, ownerRole))
		}
		_, err := tx.Exec(ctx, `INSERT INTO table_registry (object_id, schema_name, table_name, created_at) VALUES ($1,$2,$3,$4)
ON CONFLICT (object_id) DO NOTHING`, obj.ID, schema, table, time.Now().UTC())
		return err
	default:
		return nil
	}
}

func safeRoleName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			continue
		}
		return false
	}
	return strings.HasPrefix(name, "syna_")
}

func tableSchemaName(objectID string) string {
	sanitized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return '_'
	}, objectID)
	name := "syna_t_" + sanitized
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

func safeIdent(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for i, c := range name {
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
		if i == 0 && (c < 'a' || c > 'z') {
			return false
		}
		if !ok {
			return false
		}
	}
	return true
}

func storageFromObject(obj models.DataObject) models.Storage {
	kind := models.StorageKindKnowledge
	return models.Storage{
		ID: obj.ID, Slug: obj.Slug, Name: obj.Name, OwnerSubjectKey: obj.OwnerSubjectKey,
		Visibility: obj.Visibility, DefaultAccess: obj.DefaultAccess, Kind: kind,
		S3Bucket: obj.S3Bucket, S3Prefix: obj.S3Prefix, Status: obj.Status,
		CreatedAt: obj.CreatedAt, UpdatedAt: obj.UpdatedAt, ArchivedAt: obj.ArchivedAt,
	}
}

func (s *Store) GetObject(ctx context.Context, id string) (models.DataObject, bool, error) {
	if !s.useDB {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if s.objects != nil {
			if v, ok := s.objects[id]; ok {
				return v, true, nil
			}
		}
		if st, ok := s.storages[id]; ok {
			return objectFromStorage(st), true, nil
		}
		return models.DataObject{}, false, nil
	}
	row := s.pool.QueryRow(ctx, `SELECT id, kind, slug, name, owner_subject_key, visibility, default_access, status, backend, config_json::text, COALESCE(s3_bucket,''), s3_prefix, created_at, updated_at, archived_at
FROM data_objects WHERE id=$1`, id)
	obj, err := scanObject(row)
	if err == pgx.ErrNoRows {
		// Fall back to legacy storages rows not yet copied.
		st, ok, err2 := s.GetStorage(ctx, id)
		if err2 != nil || !ok {
			return models.DataObject{}, false, err2
		}
		return objectFromStorage(st), true, nil
	}
	if err != nil {
		return models.DataObject{}, false, err
	}
	return obj, true, nil
}

func objectFromStorage(st models.Storage) models.DataObject {
	return models.DataObject{
		ID: st.ID, Kind: models.ObjectKindStorage, Slug: st.Slug, Name: st.Name,
		OwnerSubjectKey: st.OwnerSubjectKey, Visibility: st.Visibility, DefaultAccess: st.DefaultAccess,
		Status: st.Status, Backend: "postgres", Config: map[string]any{}, S3Bucket: st.S3Bucket, S3Prefix: st.S3Prefix,
		CreatedAt: st.CreatedAt, UpdatedAt: st.UpdatedAt, ArchivedAt: st.ArchivedAt,
	}
}

type objectScanner interface {
	Scan(dest ...any) error
}

func scanObject(row objectScanner) (models.DataObject, error) {
	var obj models.DataObject
	var kind, vis, access, status, cfg string
	if err := row.Scan(&obj.ID, &kind, &obj.Slug, &obj.Name, &obj.OwnerSubjectKey, &vis, &access, &status, &obj.Backend, &cfg, &obj.S3Bucket, &obj.S3Prefix, &obj.CreatedAt, &obj.UpdatedAt, &obj.ArchivedAt); err != nil {
		return models.DataObject{}, err
	}
	obj.Kind = models.ObjectKind(kind)
	obj.Visibility = models.Visibility(vis)
	obj.DefaultAccess = models.AccessMode(access)
	obj.Status = models.StorageStatus(status)
	obj.Config = map[string]any{}
	_ = json.Unmarshal([]byte(cfg), &obj.Config)
	return obj, nil
}

func (s *Store) ListObjects(ctx context.Context, kind models.ObjectKind) ([]models.DataObject, error) {
	if !s.useDB {
		s.mu.RLock()
		defer s.mu.RUnlock()
		out := []models.DataObject{}
		for _, o := range s.objects {
			if kind == "" || o.Kind == kind {
				out = append(out, o)
			}
		}
		if kind == "" || kind == models.ObjectKindStorage {
			for _, st := range s.storages {
				if _, ok := s.objects[st.ID]; !ok {
					out = append(out, objectFromStorage(st))
				}
			}
		}
		return out, nil
	}
	q := `SELECT id, kind, slug, name, owner_subject_key, visibility, default_access, status, backend, config_json::text, COALESCE(s3_bucket,''), s3_prefix, created_at, updated_at, archived_at FROM data_objects`
	args := []any{}
	if kind != "" {
		q += ` WHERE kind=$1`
		args = append(args, string(kind))
	}
	q += ` ORDER BY updated_at DESC`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.DataObject{}
	for rows.Next() {
		obj, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	return out, rows.Err()
}

func (s *Store) ListAccessibleObjects(ctx context.Context, kind models.ObjectKind, f AccessibleStorageFilter) ([]models.DataObject, error) {
	all, err := s.ListObjects(ctx, kind)
	if err != nil {
		return nil, err
	}
	if f.PlatformAdmin {
		return all, nil
	}
	acl, err := s.ObjectACLForSubject(ctx, f.SubjectKeys)
	if err != nil {
		return nil, err
	}
	allowed := map[string]struct{}{}
	for _, b := range acl {
		allowed[b.ObjectID] = struct{}{}
	}
	if f.HasToken {
		tokenSet := map[string]struct{}{}
		for _, id := range f.TokenStorageIDs {
			tokenSet[id] = struct{}{}
		}
		out := []models.DataObject{}
		for _, o := range all {
			if _, ok := tokenSet[o.ID]; ok {
				out = append(out, o)
			}
		}
		return out, nil
	}
	out := []models.DataObject{}
	for _, o := range all {
		if _, ok := allowed[o.ID]; ok {
			out = append(out, o)
		}
	}
	return out, nil
}

func (s *Store) DeleteObject(ctx context.Context, objectID string) error {
	obj, ok, err := s.GetObject(ctx, objectID)
	if err != nil || !ok {
		return err
	}
	if !s.useDB {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.objects, objectID)
		for id, b := range s.objectACL {
			if b.ObjectID == objectID {
				delete(s.objectACL, id)
			}
		}
		if obj.Kind == models.ObjectKindStorage {
			delete(s.storages, objectID)
		}
		return nil
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM data_objects WHERE id=$1`, objectID)
	if err != nil {
		return err
	}
	if obj.Kind == models.ObjectKindStorage {
		_, _ = s.pool.Exec(ctx, `DELETE FROM storages WHERE id=$1`, objectID)
	}
	return nil
}

func (s *Store) UpdateObjectConfig(ctx context.Context, objectID string, config map[string]any) (models.DataObject, error) {
	obj, ok, err := s.GetObject(ctx, objectID)
	if err != nil {
		return models.DataObject{}, err
	}
	if !ok {
		return models.DataObject{}, errors.New("object not found")
	}
	if config == nil {
		config = map[string]any{}
	}
	obj.Config = config
	obj.UpdatedAt = time.Now().UTC()
	raw, _ := json.Marshal(config)
	if !s.useDB {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.objects[objectID] = obj
		return obj, nil
	}
	_, err = s.pool.Exec(ctx, `UPDATE data_objects SET config_json=$1::jsonb, updated_at=$2 WHERE id=$3`, string(raw), obj.UpdatedAt, objectID)
	if err != nil {
		return models.DataObject{}, err
	}
	return obj, nil
}

func (s *Store) UpsertObjectACL(ctx context.Context, b models.ObjectACLBinding) (models.ObjectACLBinding, error) {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now().UTC()
	}
	if !s.useDB {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.objectACL == nil {
			s.objectACL = map[string]models.ObjectACLBinding{}
		}
		s.objectACL[b.ID] = b
		return b, nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO object_acl_bindings (id, object_id, subject_key, role, granted_by, expires_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (object_id, subject_key, role) DO UPDATE SET granted_by=EXCLUDED.granted_by, expires_at=EXCLUDED.expires_at`,
		b.ID, b.ObjectID, b.SubjectKey, string(b.Role), b.GrantedBy, b.ExpiresAt, b.CreatedAt)
	return b, err
}

func (s *Store) ObjectACLForSubject(ctx context.Context, subjectKeys []string) ([]models.ObjectACLBinding, error) {
	if !s.useDB {
		s.mu.RLock()
		defer s.mu.RUnlock()
		out := []models.ObjectACLBinding{}
		for _, b := range s.objectACL {
			if slices.Contains(subjectKeys, b.SubjectKey) {
				out = append(out, b)
			}
		}
		// Bridge legacy storage ACL.
		for _, b := range s.acl {
			if slices.Contains(subjectKeys, b.SubjectKey) {
				out = append(out, models.ObjectACLBinding{
					ID: b.ID, ObjectID: b.StorageID, SubjectKey: b.SubjectKey, Role: b.Role,
					GrantedBy: b.GrantedBy, ExpiresAt: b.ExpiresAt, CreatedAt: b.CreatedAt,
				})
			}
		}
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, object_id, subject_key, role, granted_by, expires_at, created_at FROM object_acl_bindings WHERE subject_key = ANY($1)`, subjectKeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ObjectACLBinding{}
	for rows.Next() {
		var b models.ObjectACLBinding
		var role string
		if err := rows.Scan(&b.ID, &b.ObjectID, &b.SubjectKey, &role, &b.GrantedBy, &b.ExpiresAt, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.Role = models.StorageRole(role)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) ObjectACLForObject(ctx context.Context, objectID string) ([]models.ObjectACLBinding, error) {
	if !s.useDB {
		s.mu.RLock()
		defer s.mu.RUnlock()
		out := []models.ObjectACLBinding{}
		for _, b := range s.objectACL {
			if b.ObjectID == objectID {
				out = append(out, b)
			}
		}
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, object_id, subject_key, role, granted_by, expires_at, created_at FROM object_acl_bindings WHERE object_id=$1`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ObjectACLBinding{}
	for rows.Next() {
		var b models.ObjectACLBinding
		var role string
		if err := rows.Scan(&b.ID, &b.ObjectID, &b.SubjectKey, &role, &b.GrantedBy, &b.ExpiresAt, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.Role = models.StorageRole(role)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) TokenObjects(ctx context.Context, tokenID string) ([]models.AccessTokenObject, error) {
	if !s.useDB {
		s.mu.RLock()
		defer s.mu.RUnlock()
		out := []models.AccessTokenObject{}
		for _, sc := range s.tokenObjectScopes {
			if sc.TokenID == tokenID {
				out = append(out, sc)
			}
		}
		if len(out) == 0 {
			for _, sc := range s.tokenScopes {
				if sc.TokenID == tokenID {
					out = append(out, models.AccessTokenObject{
						TokenID: sc.TokenID, ObjectID: sc.StorageID, MaxMode: sc.MaxMode,
						MethodAllowlist: sc.ToolAllowlist, CreatedAt: sc.CreatedAt,
					})
				}
			}
		}
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT token_id, object_id, max_mode, method_allowlist, created_at FROM access_token_objects WHERE token_id=$1`, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.AccessTokenObject{}
	for rows.Next() {
		var sc models.AccessTokenObject
		var mode string
		var methods []string
		if err := rows.Scan(&sc.TokenID, &sc.ObjectID, &mode, &methods, &sc.CreatedAt); err != nil {
			return nil, err
		}
		sc.MaxMode = models.AccessMode(mode)
		sc.MethodAllowlist = methods
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		legacy, err := s.TokenStorages(ctx, tokenID)
		if err != nil {
			return nil, err
		}
		for _, sc := range legacy {
			out = append(out, models.AccessTokenObject{
				TokenID: sc.TokenID, ObjectID: sc.StorageID, MaxMode: sc.MaxMode,
				MethodAllowlist: sc.ToolAllowlist, CreatedAt: sc.CreatedAt,
			})
		}
	}
	return out, nil
}

func (s *Store) ReplaceTokenObjects(ctx context.Context, tokenID string, scopes []models.AccessTokenObject) error {
	if !s.useDB {
		s.mu.Lock()
		defer s.mu.Unlock()
		filtered := s.tokenObjectScopes[:0]
		for _, sc := range s.tokenObjectScopes {
			if sc.TokenID != tokenID {
				filtered = append(filtered, sc)
			}
		}
		s.tokenObjectScopes = append(filtered, scopes...)
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM access_token_objects WHERE token_id=$1`, tokenID); err != nil {
		return err
	}
	for _, sc := range scopes {
		if _, err := tx.Exec(ctx, `INSERT INTO access_token_objects (token_id, object_id, max_mode, method_allowlist, created_at)
VALUES ($1,$2,$3,$4,$5)`, tokenID, sc.ObjectID, string(sc.MaxMode), sc.MethodAllowlist, time.Now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func roleNameForSubject(subjectKey string) string {
	id := subjectID(subjectKey)
	return "syna_user_" + id
}

func roleNameForToken(tokenID string) string {
	sanitized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, strings.ToLower(tokenID))
	name := "syna_tok_" + sanitized
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

func (s *Store) EnsureUserRole(ctx context.Context, subjectKey string) error {
	if !s.useDB || subjectKey == "" {
		return nil
	}
	role := roleNameForSubject(subjectKey)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(`DO $$ BEGIN CREATE ROLE %s NOLOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END $$`, role))
	if err != nil {
		return err
	}
	_, _ = s.pool.Exec(ctx, fmt.Sprintf(`GRANT %s TO CURRENT_USER`, role))
	_, _ = s.pool.Exec(ctx, `INSERT INTO data_plane_roles (role_name, kind, subject_key, created_at) VALUES ($1,'user',$2,$3)
ON CONFLICT (role_name) DO NOTHING`, role, subjectKey, time.Now().UTC())
	return nil
}

func (s *Store) EnsureTokenRole(ctx context.Context, tokenID, subjectKey string) error {
	if !s.useDB || tokenID == "" {
		return nil
	}
	role := roleNameForToken(tokenID)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(`DO $$ BEGIN CREATE ROLE %s NOLOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END $$`, role))
	if err != nil {
		return err
	}
	_, _ = s.pool.Exec(ctx, fmt.Sprintf(`GRANT %s TO CURRENT_USER`, role))
	_, _ = s.pool.Exec(ctx, `INSERT INTO data_plane_roles (role_name, kind, subject_key, token_id, created_at) VALUES ($1,'token',$2,$3,$4)
ON CONFLICT (role_name) DO NOTHING`, role, subjectKey, tokenID, time.Now().UTC())
	return nil
}

func (s *Store) DropTokenRole(ctx context.Context, tokenID string) error {
	if !s.useDB || tokenID == "" {
		return nil
	}
	role := roleNameForToken(tokenID)
	_, _ = s.pool.Exec(ctx, fmt.Sprintf(`DROP ROLE IF EXISTS %s`, role))
	_, _ = s.pool.Exec(ctx, `DELETE FROM data_plane_roles WHERE role_name=$1`, role)
	return nil
}

func (s *Store) DataPlaneRoleName(p models.Principal, token *models.AccessToken) string {
	if token != nil {
		return roleNameForToken(token.ID)
	}
	return roleNameForSubject(models.SubjectKeyForPrincipal(p))
}

// Service methods for objects.

func (s *Service) GetObject(ctx context.Context, id string) (models.DataObject, bool, error) {
	return s.store.GetObject(ctx, id)
}

func (s *Service) UpdateObjectConfig(ctx context.Context, objectID string, config map[string]any) (models.DataObject, error) {
	return s.store.UpdateObjectConfig(ctx, objectID, config)
}

func (s *Service) DataPlaneRole(ctx context.Context, p models.Principal, token *models.AccessToken) (string, error) {
	role := s.store.DataPlaneRoleName(p, token)
	if token != nil {
		_ = s.store.EnsureTokenRole(ctx, token.ID, token.OwnerSubjectKey)
	} else {
		_ = s.store.EnsureUserRole(ctx, models.SubjectKeyForPrincipal(p))
	}
	return role, nil
}

func (s *Service) CanAccessObject(ctx context.Context, p models.Principal, token *models.AccessToken, scopes []models.AccessTokenObject, objectID, method string) (models.EffectiveObjectAccess, bool, error) {
	obj, ok, err := s.store.GetObject(ctx, objectID)
	if err != nil || !ok {
		return models.EffectiveObjectAccess{}, false, err
	}
	if !dataobj.HasMethod(obj.Kind, method) {
		return models.EffectiveObjectAccess{}, false, nil
	}
	acl, err := s.store.ObjectACLForSubject(ctx, SubjectKeys(p))
	if err != nil {
		return models.EffectiveObjectAccess{}, false, err
	}
	eff, allowed := evaluateObjectAccess(p, token, scopes, acl, obj, method)
	return eff, allowed, nil
}

func evaluateObjectAccess(p models.Principal, token *models.AccessToken, scopes []models.AccessTokenObject, acl []models.ObjectACLBinding, obj models.DataObject, method string) (models.EffectiveObjectAccess, bool) {
	userMethods := map[string]struct{}{}
	hasACL := false
	for _, b := range acl {
		if b.ObjectID != obj.ID {
			continue
		}
		hasACL = true
		for _, m := range methodsForRole(obj.Kind, b.Role) {
			userMethods[m] = struct{}{}
		}
	}
	mode := models.AccessModeReadWrite
	if token != nil {
		mode = models.AccessModeNone
		for _, scope := range scopes {
			if scope.ObjectID == obj.ID {
				mode = intersectMode(token.Mode, scope.MaxMode)
				if len(scope.MethodAllowlist) > 0 && !slices.Contains(scope.MethodAllowlist, method) && !slices.Contains(scope.MethodAllowlist, string(obj.Kind)+"_"+method) {
					return models.EffectiveObjectAccess{}, false
				}
				break
			}
		}
		if mode == models.AccessModeNone {
			return models.EffectiveObjectAccess{}, false
		}
	} else if !hasACL && strutil.Contains(p.Scopes, "platform_admin") {
		for _, m := range dataobj.Methods(obj.Kind) {
			userMethods[m] = struct{}{}
		}
	}
	if dataobj.IsMutating(method) && mode == models.AccessModeRead {
		return models.EffectiveObjectAccess{}, false
	}
	if token != nil {
		allowedByMode := methodsForMode(obj.Kind, mode)
		filtered := map[string]struct{}{}
		for m := range userMethods {
			if slices.Contains(allowedByMode, m) {
				filtered[m] = struct{}{}
			}
		}
		// Token callers also need ACL (or be owner path via EnsurePrincipal).
		if len(userMethods) == 0 {
			// Allow token if scoped and mode matches even when ACL empty (owner minted token).
			for _, m := range allowedByMode {
				filtered[m] = struct{}{}
			}
		}
		userMethods = filtered
	}
	_, ok := userMethods[method]
	eff := models.EffectiveObjectAccess{
		ObjectID: obj.ID, Kind: obj.Kind, SubjectKey: models.SubjectKeyForPrincipal(p), Mode: mode,
	}
	if token != nil {
		eff.TokenID = token.ID
	}
	for m := range userMethods {
		eff.Methods = append(eff.Methods, m)
	}
	return eff, ok
}

func methodsForRole(kind models.ObjectKind, role models.StorageRole) []string {
	all := dataobj.Methods(kind)
	switch role {
	case models.RoleStorageOwner, models.RoleStorageAdmin, models.RoleStorageWriter:
		return all
	case models.RoleStorageReader:
		out := []string{}
		for _, m := range all {
			if !dataobj.IsMutating(m) {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func methodsForMode(kind models.ObjectKind, mode models.AccessMode) []string {
	switch mode {
	case models.AccessModeReadWrite:
		return dataobj.Methods(kind)
	case models.AccessModeRead:
		out := []string{}
		for _, m := range dataobj.Methods(kind) {
			if !dataobj.IsMutating(m) {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func (s *Service) AvailableObjects(ctx context.Context, p models.Principal, token *models.AccessToken, scopes []models.AccessTokenObject, kind models.ObjectKind) ([]models.DataObject, map[string]models.EffectiveObjectAccess, error) {
	filter := AccessibleStorageFilter{
		SubjectKeys:   SubjectKeys(p),
		PlatformAdmin: token == nil && strutil.Contains(p.Scopes, "platform_admin"),
		HasToken:      token != nil,
	}
	if token != nil {
		filter.TokenStorageIDs = make([]string, 0, len(scopes))
		for _, sc := range scopes {
			filter.TokenStorageIDs = append(filter.TokenStorageIDs, sc.ObjectID)
		}
	}
	candidates, err := s.store.ListAccessibleObjects(ctx, kind, filter)
	if err != nil {
		return nil, nil, err
	}
	acl, err := s.store.ObjectACLForSubject(ctx, SubjectKeys(p))
	if err != nil {
		return nil, nil, err
	}
	out := []models.DataObject{}
	accessBy := map[string]models.EffectiveObjectAccess{}
	for _, obj := range candidates {
		methods := dataobj.Methods(obj.Kind)
		if len(methods) == 0 {
			continue
		}
		// Use first non-mutating method if available, else first method, to probe readability.
		probe := methods[0]
		for _, m := range methods {
			if !dataobj.IsMutating(m) {
				probe = m
				break
			}
		}
		eff, ok := evaluateObjectAccess(p, token, scopes, acl, obj, probe)
		if ok {
			out = append(out, obj)
			accessBy[obj.ID] = eff
		}
	}
	return out, accessBy, nil
}
