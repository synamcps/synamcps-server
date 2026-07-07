package access

import (
	"testing"

	"github.com/synamcps/synamcps-server/internal/models"
)

func TestCanWriteVisibility(t *testing.T) {
	p := models.Principal{UserID: "u1", Groups: []string{"ops"}}
	if !CanWriteVisibility(p, models.VisibilityPersonal, nil) {
		t.Fatal("personal write should be allowed")
	}
	if CanWriteVisibility(p, models.VisibilityPublic, nil) {
		t.Fatal("public write without scope denied")
	}
	pub := models.Principal{UserID: "u1", Scopes: []string{"knowledge.write.public"}}
	if !CanWriteVisibility(pub, models.VisibilityPublic, nil) {
		t.Fatal("public write with scope allowed")
	}
	if !CanWriteVisibility(p, models.VisibilityGroup, []string{"ops"}) {
		t.Fatal("group member can write to group visibility")
	}
}

func TestCanDeleteDocument(t *testing.T) {
	owner := models.Principal{UserID: "u1"}
	doc := models.DocumentRecord{OwnerID: "u1", Visibility: models.VisibilityPersonal}
	if !CanDeleteDocument(owner, doc) {
		t.Fatal("owner can delete")
	}
	other := models.Principal{UserID: "u2"}
	if CanDeleteDocument(other, doc) {
		t.Fatal("non-owner cannot delete personal")
	}
	pubDoc := models.DocumentRecord{OwnerID: "u1", Visibility: models.VisibilityPublic}
	admin := models.Principal{UserID: "u2", Scopes: []string{"knowledge.delete.public"}}
	if !CanDeleteDocument(admin, pubDoc) {
		t.Fatal("public delete scope allows deleting public doc")
	}
}
