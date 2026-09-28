package access

import (
	"context"
	"testing"

	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
	"github.com/synamcps/synamcps-server/internal/variable"
)

func TestCrossObjectAccessDenied(t *testing.T) {
	store := NewInMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	owner := models.Principal{SubjectKey: "user:owner", Scopes: []string{}}
	other := models.Principal{SubjectKey: "user:other", Scopes: []string{}}

	a, err := store.CreateObject(ctx, models.DataObject{
		Kind: models.ObjectKindVariable, Name: "a", OwnerSubjectKey: models.SubjectKeyForPrincipal(owner),
	}, models.SubjectKeyForPrincipal(owner))
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateObject(ctx, models.DataObject{
		Kind: models.ObjectKindVariable, Name: "b", OwnerSubjectKey: models.SubjectKeyForPrincipal(other),
	}, models.SubjectKeyForPrincipal(other))
	if err != nil {
		t.Fatal(err)
	}

	opener := policy.NewOpener(svc, nil)
	_, err = opener.Open(ctx, other, models.APIAccessContext{Principal: other}, a.ID, "get")
	if err != policy.ErrForbidden {
		t.Fatalf("expected forbidden for other on A's object, got %v", err)
	}
	_, err = opener.Open(ctx, owner, models.APIAccessContext{Principal: owner}, b.ID, "get")
	if err != policy.ErrForbidden {
		t.Fatalf("expected forbidden for owner on B's object, got %v", err)
	}

	scope, err := opener.Open(ctx, owner, models.APIAccessContext{Principal: owner}, a.ID, "set")
	if err != nil {
		t.Fatal(err)
	}
	vs := variable.NewService()
	if _, err := vs.Set(ctx, scope, "k", "", map[string]any{"v": 1}); err != nil {
		t.Fatal(err)
	}
	scope2, err := opener.Open(ctx, owner, models.APIAccessContext{Principal: owner}, a.ID, "get")
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := vs.Get(ctx, scope2, "k", "")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Payload["v"] != 1 {
		t.Fatalf("payload=%v", got.Payload)
	}
}

func TestTokenCannotNameForeignObject(t *testing.T) {
	store := NewInMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	owner := models.Principal{SubjectKey: "user:tokowner"}
	obj, err := store.CreateObject(ctx, models.DataObject{
		Kind: models.ObjectKindQueue, Name: "q", OwnerSubjectKey: models.SubjectKeyForPrincipal(owner),
	}, models.SubjectKeyForPrincipal(owner))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateObject(ctx, models.DataObject{
		Kind: models.ObjectKindQueue, Name: "q2", OwnerSubjectKey: "user:someone",
	}, "user:someone")
	if err != nil {
		t.Fatal(err)
	}
	token := &models.AccessToken{ID: "tok1", OwnerSubjectKey: models.SubjectKeyForPrincipal(owner), Mode: models.AccessModeReadWrite}
	scopes := []models.AccessTokenObject{{TokenID: "tok1", ObjectID: obj.ID, MaxMode: models.AccessModeReadWrite}}
	opener := policy.NewOpener(svc, nil)
	ac := models.APIAccessContext{Principal: owner, AccessToken: token, AllowedObjects: scopes}
	if _, err := opener.Open(ctx, owner, ac, foreign.ID, "enqueue"); err != policy.ErrForbidden {
		t.Fatalf("token scoped to A must not open B: %v", err)
	}
	if _, err := opener.Open(ctx, owner, ac, obj.ID, "enqueue"); err != nil {
		t.Fatal(err)
	}
	if _, err := opener.Open(ctx, owner, ac, obj.ID, "set"); err != policy.ErrUnknownMethod {
		t.Fatalf("queue object must reject variable method: %v", err)
	}
}
