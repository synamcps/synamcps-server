package policy

import (
	"context"
	"testing"

	"github.com/synamcps/synamcps-server/internal/models"
)

type fakeAccess struct {
	obj models.DataObject
	ok  bool
}

func (f fakeAccess) GetObject(ctx context.Context, id string) (models.DataObject, bool, error) {
	if !f.ok || f.obj.ID != id {
		return models.DataObject{}, false, nil
	}
	return f.obj, true, nil
}

func (f fakeAccess) CanAccessObject(ctx context.Context, p models.Principal, token *models.AccessToken, scopes []models.AccessTokenObject, objectID, method string) (models.EffectiveObjectAccess, bool, error) {
	if !f.ok || f.obj.ID != objectID {
		return models.EffectiveObjectAccess{}, false, nil
	}
	return models.EffectiveObjectAccess{ObjectID: objectID, Kind: f.obj.Kind, Methods: []string{method}}, true, nil
}

func (f fakeAccess) DataPlaneRole(ctx context.Context, p models.Principal, token *models.AccessToken) (string, error) {
	return "", nil
}

func TestOpenRejectsUnknownMethod(t *testing.T) {
	o := NewOpener(fakeAccess{ok: true, obj: models.DataObject{ID: "v1", Kind: models.ObjectKindVariable}}, nil)
	_, err := o.Open(context.Background(), models.Principal{}, models.APIAccessContext{}, "v1", "enqueue")
	if err != ErrUnknownMethod {
		t.Fatalf("got %v", err)
	}
}

func TestOpenAllowsKnownMethod(t *testing.T) {
	o := NewOpener(fakeAccess{ok: true, obj: models.DataObject{ID: "v1", Kind: models.ObjectKindVariable}}, nil)
	scope, err := o.Open(context.Background(), models.Principal{}, models.APIAccessContext{}, "v1", "get")
	if err != nil {
		t.Fatal(err)
	}
	if scope.ObjectID() != "v1" {
		t.Fatalf("%s", scope.ObjectID())
	}
}

func TestSafeRoleName(t *testing.T) {
	if !safeRoleName("syna_user_abc") {
		t.Fatal("expected safe")
	}
	if safeRoleName("postgres") || safeRoleName("syna_;drop") || safeRoleName("") {
		t.Fatal("expected unsafe")
	}
}

