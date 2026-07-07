package access

import (
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/strutil"
)

// CanWriteVisibility checks whether a principal may create a document with the
// given visibility. Storage-level ACL must be checked separately.
func CanWriteVisibility(p models.Principal, visibility models.Visibility, groupIDs []string) bool {
	switch visibility {
	case models.VisibilityPublic:
		return strutil.Contains(p.Scopes, "knowledge.write.public") || strutil.Contains(p.Scopes, "platform_admin")
	case models.VisibilityPersonal:
		return true
	case models.VisibilityGroup:
		return strutil.Intersects(groupIDs, p.Groups)
	default:
		return false
	}
}

// CanDeleteDocument checks document-level delete rules on top of storage ACL.
func CanDeleteDocument(p models.Principal, d models.DocumentRecord) bool {
	if ownsDocument(p, d) {
		return true
	}
	switch d.Visibility {
	case models.VisibilityPublic:
		return strutil.Contains(p.Scopes, "knowledge.delete.public") || strutil.Contains(p.Scopes, "platform_admin")
	case models.VisibilityGroup:
		return strutil.Contains(p.Scopes, "knowledge.delete.group")
	default:
		return false
	}
}

func ownsDocument(p models.Principal, d models.DocumentRecord) bool {
	if d.OwnerID == "" {
		return false
	}
	return d.OwnerID == p.UserID || d.OwnerID == models.SubjectKeyForPrincipal(p)
}
