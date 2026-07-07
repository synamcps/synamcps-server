package access

import (
	"testing"

	"github.com/synamcps/synamcps-server/internal/models"
)

func TestEvaluateStorageAccess(t *testing.T) {
	storageID := "st-1"
	subjectKey := "user:oauth:alice"

	principal := func(scopes ...string) models.Principal {
		return models.Principal{
			UserID:     "alice",
			SubjectKey: subjectKey,
			AuthSource: "oauth",
			Scopes:     scopes,
		}
	}

	readerACL := []models.ACLBinding{{
		StorageID:  storageID,
		SubjectKey: subjectKey,
		Role:       models.RoleStorageReader,
	}}

	writerACL := []models.ACLBinding{{
		StorageID:  storageID,
		SubjectKey: subjectKey,
		Role:       models.RoleStorageWriter,
	}}

	token := func(mode models.AccessMode, perms ...models.StoragePermission) *models.AccessToken {
		return &models.AccessToken{
			ID:                 "tok-1",
			Mode:               mode,
			AllowedPermissions: perms,
		}
	}

	tests := []struct {
		name       string
		p          models.Principal
		token      *models.AccessToken
		scopes     []models.AccessTokenStorage
		acl        []models.ACLBinding
		permission models.StoragePermission
		wantOK     bool
		wantMode   models.AccessMode
	}{
		{
			name:       "reader can read",
			p:          principal(),
			acl:        readerACL,
			permission: models.PermissionDocumentRead,
			wantOK:     true,
			wantMode:   models.AccessModeReadWrite,
		},
		{
			name:       "reader cannot create",
			p:          principal(),
			acl:        readerACL,
			permission: models.PermissionDocumentCreate,
			wantOK:     false,
		},
		{
			name:       "writer can create",
			p:          principal(),
			acl:        writerACL,
			permission: models.PermissionDocumentCreate,
			wantOK:     true,
		},
		{
			name:       "no acl denied",
			p:          principal(),
			acl:        nil,
			permission: models.PermissionDocumentRead,
			wantOK:     false,
		},
		{
			name:       "platform_admin without acl can read",
			p:          principal("platform_admin"),
			acl:        nil,
			permission: models.PermissionDocumentRead,
			wantOK:     true,
		},
		{
			name:       "token read mode limits write",
			p:          principal(),
			token:      token(models.AccessModeRead),
			scopes:     []models.AccessTokenStorage{{StorageID: storageID, MaxMode: models.AccessModeReadWrite}},
			acl:        writerACL,
			permission: models.PermissionDocumentCreate,
			wantOK:     false,
			wantMode:   models.AccessModeRead,
		},
		{
			name:       "token rw mode allows write",
			p:          principal(),
			token:      token(models.AccessModeReadWrite),
			scopes:     []models.AccessTokenStorage{{StorageID: storageID, MaxMode: models.AccessModeReadWrite}},
			acl:        writerACL,
			permission: models.PermissionDocumentCreate,
			wantOK:     true,
			wantMode:   models.AccessModeReadWrite,
		},
		{
			name:       "token scope intersection read",
			p:          principal(),
			token:      token(models.AccessModeReadWrite),
			scopes:     []models.AccessTokenStorage{{StorageID: storageID, MaxMode: models.AccessModeRead}},
			acl:        writerACL,
			permission: models.PermissionDocumentRead,
			wantOK:     true,
			wantMode:   models.AccessModeRead,
		},
		{
			name:       "token missing storage scope denied",
			p:          principal(),
			token:      token(models.AccessModeReadWrite),
			scopes:     []models.AccessTokenStorage{{StorageID: "other", MaxMode: models.AccessModeReadWrite}},
			acl:        writerACL,
			permission: models.PermissionDocumentRead,
			wantOK:     false,
			wantMode:   models.AccessModeNone,
		},
		{
			name:       "allowed permissions filter",
			p:          principal(),
			token:      token(models.AccessModeReadWrite, models.PermissionDocumentRead),
			scopes:     []models.AccessTokenStorage{{StorageID: storageID, MaxMode: models.AccessModeReadWrite}},
			acl:        writerACL,
			permission: models.PermissionDocumentCreate,
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eff, ok := evaluateStorageAccess(tt.p, tt.token, tt.scopes, tt.acl, storageID, tt.permission)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantMode != "" && eff.Mode != tt.wantMode {
				t.Fatalf("mode = %q, want %q", eff.Mode, tt.wantMode)
			}
		})
	}
}

func TestIntersectMode(t *testing.T) {
	tests := []struct {
		a, b models.AccessMode
		want models.AccessMode
	}{
		{models.AccessModeReadWrite, models.AccessModeRead, models.AccessModeRead},
		{models.AccessModeReadWrite, models.AccessModeNone, models.AccessModeNone},
		{models.AccessModeRead, models.AccessModeReadWrite, models.AccessModeRead},
	}
	for _, tt := range tests {
		if got := intersectMode(tt.a, tt.b); got != tt.want {
			t.Fatalf("intersectMode(%q,%q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}
