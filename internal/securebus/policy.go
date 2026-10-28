package securebus

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

//go:embed policy_schema.json
var PolicySchemaJSON []byte

const PolicySchemaID = "synamcps.securebus.policy/v1"

type AgentRef struct {
	KeyID     string `json:"keyId"`
	Alg       string `json:"alg"`
	PublicKey string `json:"publicKey"`
}

type Rules struct {
	Publish   []string `json:"publish"`
	Attest    []string `json:"attest"`
	Subscribe []string `json:"subscribe"`
}

type AttestationPolicy struct {
	Statuses        []string `json:"statuses"`
	CommentMaxBytes int      `json:"commentMaxBytes"`
	Supersede       bool     `json:"supersede"`
}

type ConditionRequire struct {
	Agent  string `json:"agent"`
	Status string `json:"status"`
}

type Condition struct {
	Authors  []string           `json:"authors,omitempty"`
	Require  []ConditionRequire `json:"require,omitempty"`
	MinOk    int                `json:"minOk,omitempty"`
	AnyDeny  *bool              `json:"anyDeny,omitempty"`
	Mode     string             `json:"mode,omitempty"`
}

type Document struct {
	Schema      string               `json:"schema"`
	TopicID     string               `json:"topicId"`
	Version     int                  `json:"version"`
	IssuedAt    string               `json:"issuedAt"`
	Chain       bool                 `json:"chain"`
	Agents      map[string]AgentRef  `json:"agents"`
	Rules       Rules                `json:"rules"`
	Attestation AttestationPolicy    `json:"attestation,omitempty"`
	Conditions  map[string]Condition `json:"conditions,omitempty"`
}

type SignedPolicy struct {
	Document  Document `json:"document"`
	Hash      string   `json:"hash"`
	Signature string   `json:"signature"`
	SignerKID string   `json:"signerKid"`
	Version   int      `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
}

func ParseDocument(raw []byte) (Document, error) {
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Document{}, err
	}
	if err := ValidateDocument(doc); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func ValidateDocument(doc Document) error {
	if doc.Schema != PolicySchemaID {
		return fmt.Errorf("unsupported policy schema %q", doc.Schema)
	}
	if doc.TopicID == "" {
		return errors.New("topicId required")
	}
	if doc.Version < 1 {
		return errors.New("version must be >= 1")
	}
	if doc.IssuedAt == "" {
		return errors.New("issuedAt required")
	}
	if len(doc.Agents) == 0 {
		return errors.New("agents required")
	}
	for name, a := range doc.Agents {
		if name == "" || a.KeyID == "" || a.PublicKey == "" {
			return fmt.Errorf("invalid agent %q", name)
		}
		if a.Alg != "ed25519" && a.Alg != "ecdsa-p256" {
			return fmt.Errorf("unsupported alg %q for agent %q", a.Alg, name)
		}
	}
	checkNames := func(role string, names []string) error {
		for _, n := range names {
			if _, ok := doc.Agents[n]; !ok {
				return fmt.Errorf("rules.%s references unknown agent %q", role, n)
			}
		}
		return nil
	}
	if err := checkNames("publish", doc.Rules.Publish); err != nil {
		return err
	}
	if err := checkNames("attest", doc.Rules.Attest); err != nil {
		return err
	}
	if err := checkNames("subscribe", doc.Rules.Subscribe); err != nil {
		return err
	}
	if doc.Attestation.CommentMaxBytes == 0 {
		doc.Attestation.CommentMaxBytes = 4096
	}
	if len(doc.Attestation.Statuses) == 0 {
		doc.Attestation.Statuses = []string{"ok", "deny"}
	}
	for name, c := range doc.Conditions {
		for _, a := range c.Authors {
			if _, ok := doc.Agents[a]; !ok {
				return fmt.Errorf("condition %q author unknown agent %q", name, a)
			}
		}
		for _, r := range c.Require {
			if _, ok := doc.Agents[r.Agent]; !ok {
				return fmt.Errorf("condition %q require unknown agent %q", name, r.Agent)
			}
			if r.Status != "ok" && r.Status != "deny" {
				return fmt.Errorf("condition %q invalid status %q", name, r.Status)
			}
		}
	}
	return nil
}

func (d Document) AgentNameForKey(keyID string) (string, bool) {
	for name, a := range d.Agents {
		if a.KeyID == keyID {
			return name, true
		}
	}
	return "", false
}

func (d Document) May(rule string, agentName string) bool {
	var list []string
	switch rule {
	case "publish":
		list = d.Rules.Publish
	case "attest":
		list = d.Rules.Attest
	case "subscribe":
		list = d.Rules.Subscribe
	default:
		return false
	}
	return slices.Contains(list, agentName)
}

func (d Document) AsMap() map[string]any {
	raw, _ := json.Marshal(d)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
