package access

import "github.com/synamcps/synamcps-server/internal/models"

// CanWriteVisibility checks whether a principal may create a document with the
// given visibility. Storage-level ACL must be checked separately.
func CanWriteVisibility(p models.Principal, visibility models.Visibility, groupIDs []string) bool {
	switch visibility {
	case models.VisibilityPublic:
		return hasScope(p.Scopes, "knowledge.write.public") || hasScope(p.Scopes, "platform_admin")
	case models.VisibilityPersonal:
		return true
	case models.VisibilityGroup:
		return intersectGroups(groupIDs, p.Groups)
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
		return hasScope(p.Scopes, "knowledge.delete.public") || hasScope(p.Scopes, "platform_admin")
	case models.VisibilityGroup:
		return hasScope(p.Scopes, "knowledge.delete.group")
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

func intersectGroups(a, b []string) bool {
	set := map[string]struct{}{}
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
