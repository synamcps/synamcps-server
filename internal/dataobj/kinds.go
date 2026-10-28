package dataobj

import "github.com/synamcps/synamcps-server/internal/models"

// Methods returns the closed method set for a kind.
func Methods(kind models.ObjectKind) []string {
	switch kind {
	case models.ObjectKindStorage:
		return []string{"search", "get", "save", "delete"}
	case models.ObjectKindVariable:
		return []string{"set", "get"}
	case models.ObjectKindCounter:
		return []string{"set", "get", "increase", "decrease"}
	case models.ObjectKindTable:
		return []string{"query"}
	case models.ObjectKindQueue:
		return []string{"enqueue", "dequeue", "peek", "size"}
	case models.ObjectKindBus:
		return []string{"produce", "consume", "subscribe"}
	case models.ObjectKindSecureBus:
		return []string{"policy", "publish", "attest", "verify", "consume", "subscribe", "keys"}
	case models.ObjectKindSchedule:
		return []string{"set", "get", "list"}
	case models.ObjectKindLog:
		return []string{"add", "tail"}
	case models.ObjectKindGit:
		return []string{
			"init", "clone", "add", "commit", "status", "log", "diff",
			"branch", "checkout", "fetch", "pull", "push", "tag", "show", "remote",
		}
	default:
		return nil
	}
}

func HasMethod(kind models.ObjectKind, method string) bool {
	for _, m := range Methods(kind) {
		if m == method {
			return true
		}
	}
	return false
}

func IsMutating(method string) bool {
	switch method {
	case "get", "search", "peek", "size", "list", "tail", "status", "log", "diff", "show",
		"consume", "subscribe", "policy", "verify", "keys":
		return false
	default:
		return true
	}
}

func ValidKind(kind models.ObjectKind) bool {
	return len(Methods(kind)) > 0
}
