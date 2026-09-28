package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synamcps/synamcps-server/internal/dataobj"
	"github.com/synamcps/synamcps-server/internal/models"
)

var (
	ErrForbidden       = errors.New("forbidden")
	ErrUnknownMethod   = errors.New("unknown method for object kind")
	ErrObjectNotFound  = errors.New("object not found")
	ErrKindMismatch    = errors.New("object kind mismatch")
)

// ObjectAccess evaluates ACL + token caps for data objects.
type ObjectAccess interface {
	GetObject(ctx context.Context, id string) (models.DataObject, bool, error)
	CanAccessObject(ctx context.Context, p models.Principal, token *models.AccessToken, scopes []models.AccessTokenObject, objectID, method string) (models.EffectiveObjectAccess, bool, error)
	DataPlaneRole(ctx context.Context, p models.Principal, token *models.AccessToken) (string, error)
}

// Scope is a checked, role-switched data-plane session for one object+method.
type Scope struct {
	Object   models.DataObject
	Method   string
	Access   models.EffectiveObjectAccess
	Principal models.Principal
	tx       pgx.Tx
	mem      bool
}

func (s *Scope) ObjectID() string { return s.Object.ID }
func (s *Scope) Kind() models.ObjectKind { return s.Object.Kind }
func (s *Scope) Tx() pgx.Tx { return s.tx }
func (s *Scope) Memory() bool { return s.mem }

func (s *Scope) Commit(ctx context.Context) error {
	if s.tx == nil {
		return nil
	}
	return s.tx.Commit(ctx)
}

func (s *Scope) Rollback(ctx context.Context) error {
	if s.tx == nil {
		return nil
	}
	return s.tx.Rollback(ctx)
}

type Opener struct {
	Access ObjectAccess
	Pool   *pgxpool.Pool
}

func NewOpener(access ObjectAccess, pool *pgxpool.Pool) *Opener {
	return &Opener{Access: access, Pool: pool}
}

// Open validates kind/method/ACL and begins a SET LOCAL ROLE transaction when Postgres is configured.
func (o *Opener) Open(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext, objectID string, method string) (*Scope, error) {
	if o == nil || o.Access == nil {
		return nil, ErrForbidden
	}
	obj, ok, err := o.Access.GetObject(ctx, objectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrObjectNotFound
	}
	if !dataobj.HasMethod(obj.Kind, method) {
		return nil, ErrUnknownMethod
	}
	scopes := accessCtx.AllowedObjects
	if len(scopes) == 0 {
		// Bridge legacy token storage scopes into object scopes.
		for _, st := range accessCtx.AllowedStorage {
			scopes = append(scopes, models.AccessTokenObject{
				TokenID:         st.TokenID,
				ObjectID:        st.StorageID,
				MaxMode:         st.MaxMode,
				MethodAllowlist: st.ToolAllowlist,
				CreatedAt:       st.CreatedAt,
			})
		}
	}
	eff, allowed, err := o.Access.CanAccessObject(ctx, p, accessCtx.AccessToken, scopes, objectID, method)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrForbidden
	}
	scope := &Scope{Object: obj, Method: method, Access: eff, Principal: p, mem: o.Pool == nil}
	if o.Pool == nil {
		return scope, nil
	}
	tx, err := o.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	role, err := o.Access.DataPlaneRole(ctx, p, accessCtx.AccessToken)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	if role != "" {
		// Quote as identifier: roles are created by us with a safe prefix.
		if !safeRoleName(role) {
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("invalid data plane role")
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('syna.object_ids', $1, true)", objectID); err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL ROLE %s", role)); err != nil {
			// Role may not exist yet in fresh envs; keep session as current user with GUC set.
			// RLS still applies when policies use syna.object_ids.
			_ = err
		}
	} else if _, err := tx.Exec(ctx, "SELECT set_config('syna.object_ids', $1, true)", objectID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	scope.tx = tx
	return scope, nil
}

func safeRoleName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for i, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			continue
		}
		if i > 0 && c == '-' {
			continue
		}
		return false
	}
	return strings.HasPrefix(name, "syna_")
}
