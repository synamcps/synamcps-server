package securebus

import (
	"encoding/json"
	"errors"
	"slices"
)

var ErrUnknownCondition = errors.New("unknown condition")

type AttestationView struct {
	AgentName string
	KeyID     string
	Status    string
}

type MessageView struct {
	AuthorKeyID  string
	AuthorName   string
	Attestations []AttestationView
}

// MatchCondition evaluates a named or ad-hoc condition against a message aggregate.
func MatchCondition(cond Condition, msg MessageView) bool {
	mode := cond.Mode
	if mode == "" {
		if len(cond.Require) > 0 || cond.MinOk > 0 || cond.AnyDeny != nil {
			mode = "on_condition"
		} else {
			mode = "on_publish"
		}
	}
	if len(cond.Authors) > 0 && !slices.Contains(cond.Authors, msg.AuthorName) {
		return false
	}
	latest := map[string]string{}
	for _, a := range msg.Attestations {
		latest[a.AgentName] = a.Status
	}
	okCount := 0
	hasDeny := false
	for _, st := range latest {
		if st == "ok" {
			okCount++
		}
		if st == "deny" {
			hasDeny = true
		}
	}
	if cond.AnyDeny != nil && *cond.AnyDeny {
		return hasDeny
	}
	if cond.AnyDeny != nil && !*cond.AnyDeny && hasDeny {
		return false
	}
	for _, r := range cond.Require {
		if latest[r.Agent] != r.Status {
			return false
		}
	}
	if cond.MinOk > 0 && okCount < cond.MinOk {
		return false
	}
	if mode == "on_publish" {
		return true
	}
	if len(cond.Require) == 0 && cond.MinOk == 0 {
		return true
	}
	return true
}

func ResolveCondition(doc Document, name string, adHoc map[string]any) (Condition, error) {
	if name != "" {
		c, ok := doc.Conditions[name]
		if !ok {
			return Condition{}, ErrUnknownCondition
		}
		return c, nil
	}
	if adHoc == nil {
		return Condition{Mode: "on_publish"}, nil
	}
	raw, err := json.Marshal(adHoc)
	if err != nil {
		return Condition{}, err
	}
	var c Condition
	if err := json.Unmarshal(raw, &c); err != nil {
		return Condition{}, err
	}
	return c, nil
}
