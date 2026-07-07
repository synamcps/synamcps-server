package policy

import (
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/strutil"
)

func CanRead(p models.Principal, d models.DocumentRecord) bool {
	switch d.Visibility {
	case models.VisibilityPublic:
		return true
	case models.VisibilityPersonal:
		return d.OwnerID == p.UserID
	case models.VisibilityGroup:
		return strutil.Intersects(d.GroupIDs, p.Groups) || d.OwnerID == p.UserID
	default:
		return false
	}
}

func CanWrite(p models.Principal, visibility models.Visibility, groupIDs []string) bool {
	switch visibility {
	case models.VisibilityPublic:
		return strutil.Contains(p.Scopes, "knowledge.write.public")
	case models.VisibilityPersonal:
		return true
	case models.VisibilityGroup:
		return strutil.Intersects(groupIDs, p.Groups)
	default:
		return false
	}
}

func CanDelete(p models.Principal, d models.DocumentRecord) bool {
	if d.OwnerID == p.UserID {
		return true
	}
	if d.Visibility == models.VisibilityPublic {
		return strutil.Contains(p.Scopes, "knowledge.delete.public")
	}
	if d.Visibility == models.VisibilityGroup {
		return strutil.Contains(p.Scopes, "knowledge.delete.group")
	}
	return false
}
