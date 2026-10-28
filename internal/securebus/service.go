package securebus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/synamcps/synamcps-server/internal/agentkeys"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
)

var (
	ErrInvalidSignature = errors.New("invalid signature")
	ErrForbiddenByPolicy = errors.New("forbidden by topic policy")
	ErrStalePolicy      = errors.New("stale policy version")
	ErrKeyNotAllowed    = errors.New("key not allowed for caller")
	ErrNotFound         = errors.New("not found")
)

type KeyLookup interface {
	Get(ctx context.Context, keyID string) (agentkeys.Key, bool, error)
}

type Service struct {
	mu           sync.Mutex
	memPolicies  map[string][]SignedPolicy // objectID -> versions
	memMessages  map[string][]Message
	memAttests   map[string][]Attestation // objectID
	memEvents    map[string][]Event
	memSubs      map[string]map[string]Subscription
	memDelivered map[string]map[string]map[int64]struct{} // object -> consumer -> messageIDs
	keys         KeyLookup
	signer       *PolicySigner
	grace        time.Duration
}

type Message struct {
	ID            int64          `json:"id"`
	ObjectID      string         `json:"objectId"`
	AuthorKeyID   string         `json:"authorKeyId"`
	PolicyVersion int            `json:"policyVersion"`
	PolicyHash    string         `json:"policyHash"`
	Nonce         string         `json:"nonce"`
	Payload       map[string]any `json:"payload"`
	Hash          string         `json:"hash"`
	PrevHash      string         `json:"prevHash,omitempty"`
	Signature     string         `json:"signature"`
	CreatedAt     time.Time      `json:"createdAt"`
}

type Attestation struct {
	ID            int64     `json:"id"`
	ObjectID      string    `json:"objectId"`
	MessageID     int64     `json:"messageId"`
	SignerKeyID   string    `json:"signerKeyId"`
	PolicyVersion int       `json:"policyVersion"`
	Status        string    `json:"status"`
	Comment       string    `json:"comment"`
	MessageHash   string    `json:"messageHash"`
	Signature     string    `json:"signature"`
	CreatedAt     time.Time `json:"createdAt"`
	SupersededAt  *time.Time `json:"supersededAt,omitempty"`
}

type Event struct {
	ID            int64     `json:"id"`
	ObjectID      string    `json:"objectId"`
	Kind          string    `json:"kind"`
	MessageID     int64     `json:"messageId"`
	AttestationID *int64    `json:"attestationId,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type Subscription struct {
	Consumer    string         `json:"consumer"`
	Filter      map[string]any `json:"filter"`
	LastEventID int64          `json:"lastEventId"`
}

type PublishInput struct {
	KeyID         string
	SubjectKey    string
	TokenID       string
	PolicyVersion int
	PolicyHash    string
	Nonce         string
	CreatedAt     time.Time
	Payload       map[string]any
	Signature     string
	PrevHash      string
}

type AttestInput struct {
	KeyID         string
	SubjectKey    string
	TokenID       string
	MessageID     int64
	PolicyVersion int
	Status        string
	Comment       string
	CreatedAt     time.Time
	Signature     string
}

func NewService(keys KeyLookup, signer *PolicySigner, graceSeconds int) *Service {
	g := time.Duration(graceSeconds) * time.Second
	if g <= 0 {
		g = 5 * time.Minute
	}
	return &Service{
		memPolicies:  map[string][]SignedPolicy{},
		memMessages:  map[string][]Message{},
		memAttests:   map[string][]Attestation{},
		memEvents:    map[string][]Event{},
		memSubs:      map[string]map[string]Subscription{},
		memDelivered: map[string]map[string]map[int64]struct{}{},
		keys:         keys,
		signer:       signer,
		grace:        g,
	}
}

func (s *Service) PutPolicy(ctx context.Context, scope *policy.Scope, doc Document, createdBy string) (SignedPolicy, error) {
	if scope == nil || scope.Kind() != models.ObjectKindSecureBus {
		return SignedPolicy{}, policy.ErrKindMismatch
	}
	doc.TopicID = scope.ObjectID()
	doc.Schema = PolicySchemaID
	if doc.IssuedAt == "" {
		doc.IssuedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := ValidateDocument(doc); err != nil {
		_ = scope.Rollback(ctx)
		return SignedPolicy{}, err
	}
	for _, a := range doc.Agents {
		k, ok, err := s.keys.Get(ctx, a.KeyID)
		if err != nil {
			_ = scope.Rollback(ctx)
			return SignedPolicy{}, err
		}
		if !ok || k.RevokedAt != nil {
			_ = scope.Rollback(ctx)
			return SignedPolicy{}, fmt.Errorf("agent key %q missing or revoked", a.KeyID)
		}
	}
	next := 1
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if vers := s.memPolicies[scope.ObjectID()]; len(vers) > 0 {
			next = vers[len(vers)-1].Version + 1
		}
		doc.Version = next
		hash, sig, err := s.signer.SignDocument(doc)
		if err != nil {
			return SignedPolicy{}, err
		}
		sp := SignedPolicy{
			Document: doc, Hash: B64(hash), Signature: B64(sig),
			SignerKID: s.signer.KID, Version: next, CreatedAt: time.Now().UTC(),
		}
		s.memPolicies[scope.ObjectID()] = append(s.memPolicies[scope.ObjectID()], sp)
		_ = scope.Commit(ctx)
		return sp, nil
	}
	var maxVer *int
	_ = scope.Tx().QueryRow(ctx, `SELECT MAX(version) FROM securebus_policies WHERE object_id=$1`, scope.ObjectID()).Scan(&maxVer)
	if maxVer != nil {
		next = *maxVer + 1
	}
	doc.Version = next
	hash, sig, err := s.signer.SignDocument(doc)
	if err != nil {
		_ = scope.Rollback(ctx)
		return SignedPolicy{}, err
	}
	raw, _ := json.Marshal(doc)
	now := time.Now().UTC()
	_, err = scope.Tx().Exec(ctx, `INSERT INTO securebus_policies (object_id, version, document, hash, signature, signer_kid, created_by, created_at)
VALUES ($1,$2,$3::jsonb,$4,$5,$6,$7,$8)`, scope.ObjectID(), next, string(raw), hash, sig, s.signer.KID, createdBy, now)
	if err != nil {
		_ = scope.Rollback(ctx)
		return SignedPolicy{}, err
	}
	cfg := scope.Object.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["policyVersion"] = next
	cfgRaw, _ := json.Marshal(cfg)
	_, err = scope.Tx().Exec(ctx, `UPDATE data_objects SET config_json=$1::jsonb, updated_at=$2 WHERE id=$3`, string(cfgRaw), now, scope.ObjectID())
	if err != nil {
		_ = scope.Rollback(ctx)
		return SignedPolicy{}, err
	}
	return SignedPolicy{Document: doc, Hash: B64(hash), Signature: B64(sig), SignerKID: s.signer.KID, Version: next, CreatedAt: now}, scope.Commit(ctx)
}

func (s *Service) GetPolicy(ctx context.Context, scope *policy.Scope, version int) (SignedPolicy, error) {
	if scope == nil || scope.Kind() != models.ObjectKindSecureBus {
		return SignedPolicy{}, policy.ErrKindMismatch
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		vers := s.memPolicies[scope.ObjectID()]
		if len(vers) == 0 {
			_ = scope.Commit(ctx)
			return SignedPolicy{}, ErrNotFound
		}
		if version <= 0 {
			_ = scope.Commit(ctx)
			return vers[len(vers)-1], nil
		}
		for _, sp := range vers {
			if sp.Version == version {
				_ = scope.Commit(ctx)
				return sp, nil
			}
		}
		_ = scope.Commit(ctx)
		return SignedPolicy{}, ErrNotFound
	}
	q := `SELECT version, document::text, hash, signature, signer_kid, created_at FROM securebus_policies WHERE object_id=$1`
	args := []any{scope.ObjectID()}
	if version > 0 {
		q += ` AND version=$2`
		args = append(args, version)
	} else {
		q += ` ORDER BY version DESC LIMIT 1`
	}
	row := scope.Tx().QueryRow(ctx, q, args...)
	var sp SignedPolicy
	var raw string
	var hash, sig []byte
	if err := row.Scan(&sp.Version, &raw, &hash, &sig, &sp.SignerKID, &sp.CreatedAt); err != nil {
		_ = scope.Commit(ctx)
		return SignedPolicy{}, ErrNotFound
	}
	doc, err := ParseDocument([]byte(raw))
	if err != nil {
		_ = scope.Rollback(ctx)
		return SignedPolicy{}, err
	}
	sp.Document = doc
	sp.Hash = B64(hash)
	sp.Signature = B64(sig)
	return sp, scope.Commit(ctx)
}

func (s *Service) currentPolicy(ctx context.Context, scope *policy.Scope) (SignedPolicy, error) {
	// GetPolicy commits; for in-tx use we need a private load. Use memory path or re-open.
	if scope.Memory() || scope.Tx() == nil {
		return s.GetPolicy(ctx, scope, 0)
	}
	row := scope.Tx().QueryRow(ctx, `SELECT version, document::text, hash, signature, signer_kid, created_at
FROM securebus_policies WHERE object_id=$1 ORDER BY version DESC LIMIT 1`, scope.ObjectID())
	var sp SignedPolicy
	var raw string
	var hash, sig []byte
	if err := row.Scan(&sp.Version, &raw, &hash, &sig, &sp.SignerKID, &sp.CreatedAt); err != nil {
		return SignedPolicy{}, ErrNotFound
	}
	doc, err := ParseDocument([]byte(raw))
	if err != nil {
		return SignedPolicy{}, err
	}
	sp.Document = doc
	sp.Hash = B64(hash)
	sp.Signature = B64(sig)
	return sp, nil
}

func (s *Service) loadPolicyVersion(ctx context.Context, scope *policy.Scope, version int) (SignedPolicy, error) {
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, sp := range s.memPolicies[scope.ObjectID()] {
			if sp.Version == version {
				return sp, nil
			}
		}
		return SignedPolicy{}, ErrNotFound
	}
	row := scope.Tx().QueryRow(ctx, `SELECT version, document::text, hash, signature, signer_kid, created_at
FROM securebus_policies WHERE object_id=$1 AND version=$2`, scope.ObjectID(), version)
	var sp SignedPolicy
	var raw string
	var hash, sig []byte
	if err := row.Scan(&sp.Version, &raw, &hash, &sig, &sp.SignerKID, &sp.CreatedAt); err != nil {
		return SignedPolicy{}, ErrNotFound
	}
	doc, err := ParseDocument([]byte(raw))
	if err != nil {
		return SignedPolicy{}, err
	}
	sp.Document = doc
	sp.Hash = B64(hash)
	sp.Signature = B64(sig)
	return sp, nil
}

func (s *Service) acceptPolicyVersion(current SignedPolicy, wantVersion int, wantHash string) error {
	if wantVersion == current.Version && (wantHash == "" || wantHash == current.Hash) {
		return nil
	}
	if wantVersion == current.Version-1 && time.Since(current.CreatedAt) <= s.grace {
		return nil
	}
	return ErrStalePolicy
}

func (s *Service) Publish(ctx context.Context, scope *policy.Scope, in PublishInput) (Message, error) {
	if scope == nil || scope.Kind() != models.ObjectKindSecureBus {
		return Message{}, policy.ErrKindMismatch
	}
	key, ok, err := s.keys.Get(ctx, in.KeyID)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Message{}, err
	}
	if !ok || !key.BelongsToCaller(in.SubjectKey, in.TokenID) {
		_ = scope.Rollback(ctx)
		return Message{}, ErrKeyNotAllowed
	}
	cur, err := s.currentPolicy(ctx, scope)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Message{}, err
	}
	if err := s.acceptPolicyVersion(cur, in.PolicyVersion, in.PolicyHash); err != nil {
		_ = scope.Rollback(ctx)
		return Message{}, err
	}
	pol, err := s.loadPolicyVersion(ctx, scope, in.PolicyVersion)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Message{}, err
	}
	agentName, ok := pol.Document.AgentNameForKey(in.KeyID)
	if !ok || !pol.Document.May("publish", agentName) {
		_ = scope.Rollback(ctx)
		return Message{}, ErrForbiddenByPolicy
	}
	createdAt := in.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if in.Payload == nil {
		in.Payload = map[string]any{}
	}
	envelope := map[string]any{
		"objectId":      scope.ObjectID(),
		"policyVersion": float64(in.PolicyVersion),
		"policyHash":    in.PolicyHash,
		"authorKeyId":   in.KeyID,
		"nonce":         in.Nonce,
		"createdAt":     createdAt.Format(time.RFC3339Nano),
		"payload":       in.Payload,
	}
	if in.PrevHash != "" {
		envelope["prevHash"] = in.PrevHash
	}
	sig, err := DecodeB64(in.Signature)
	if err != nil || !VerifyAgentSig(key.Algorithm, key.PublicKey, sig, envelope) {
		_ = scope.Rollback(ctx)
		return Message{}, ErrInvalidSignature
	}
	msgHash, _ := HashCanonical(envelope)
	msg := Message{
		ObjectID: scope.ObjectID(), AuthorKeyID: in.KeyID, PolicyVersion: in.PolicyVersion,
		PolicyHash: in.PolicyHash, Nonce: in.Nonce, Payload: in.Payload,
		Hash: B64(msgHash), PrevHash: in.PrevHash, Signature: in.Signature, CreatedAt: createdAt,
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if pol.Document.Chain {
			msgs := s.memMessages[scope.ObjectID()]
			if len(msgs) > 0 {
				prev := msgs[len(msgs)-1].Hash
				if in.PrevHash == "" {
					in.PrevHash = prev
					envelope["prevHash"] = prev
					msg.PrevHash = prev
					msgHash, _ = HashCanonical(envelope)
					msg.Hash = B64(msgHash)
					if !VerifyAgentSig(key.Algorithm, key.PublicKey, sig, envelope) {
						return Message{}, errors.New("prevHash required when chain enabled")
					}
				} else if in.PrevHash != prev {
					return Message{}, errors.New("prevHash mismatch")
				}
			}
		}
		msg.ID = int64(len(s.memMessages[scope.ObjectID()]) + 1)
		s.memMessages[scope.ObjectID()] = append(s.memMessages[scope.ObjectID()], msg)
		eid := int64(len(s.memEvents[scope.ObjectID()]) + 1)
		s.memEvents[scope.ObjectID()] = append(s.memEvents[scope.ObjectID()], Event{
			ID: eid, ObjectID: scope.ObjectID(), Kind: "message_published", MessageID: msg.ID, CreatedAt: createdAt,
		})
		_ = scope.Commit(ctx)
		return msg, nil
	}
	if pol.Document.Chain {
		var prev []byte
		_ = scope.Tx().QueryRow(ctx, `SELECT hash FROM securebus_messages WHERE object_id=$1 ORDER BY id DESC LIMIT 1 FOR UPDATE`, scope.ObjectID()).Scan(&prev)
		if len(prev) > 0 {
			prevB64 := B64(prev)
			if in.PrevHash == "" || in.PrevHash != prevB64 {
				_ = scope.Rollback(ctx)
				return Message{}, errors.New("prevHash required/mismatch when chain enabled")
			}
		}
	}
	polHashBytes, _ := DecodeB64(in.PolicyHash)
	var id int64
	err = scope.Tx().QueryRow(ctx, `INSERT INTO securebus_messages
(object_id, author_key_id, policy_version, policy_hash, nonce, payload, hash, prev_hash, signature, created_at)
VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9,$10) RETURNING id`,
		scope.ObjectID(), in.KeyID, in.PolicyVersion, polHashBytes, in.Nonce, mustJSON(in.Payload), msgHash, decodeOpt(in.PrevHash), sig, createdAt).Scan(&id)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Message{}, err
	}
	msg.ID = id
	_, err = scope.Tx().Exec(ctx, `INSERT INTO securebus_events (object_id, kind, message_id, created_at) VALUES ($1,'message_published',$2,$3)`,
		scope.ObjectID(), id, createdAt)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Message{}, err
	}
	return msg, scope.Commit(ctx)
}

func decodeOpt(s string) []byte {
	if s == "" {
		return nil
	}
	b, _ := DecodeB64(s)
	return b
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func (s *Service) Attest(ctx context.Context, scope *policy.Scope, in AttestInput) (Attestation, error) {
	if scope == nil || scope.Kind() != models.ObjectKindSecureBus {
		return Attestation{}, policy.ErrKindMismatch
	}
	key, ok, err := s.keys.Get(ctx, in.KeyID)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Attestation{}, err
	}
	if !ok || !key.BelongsToCaller(in.SubjectKey, in.TokenID) {
		_ = scope.Rollback(ctx)
		return Attestation{}, ErrKeyNotAllowed
	}
	cur, err := s.currentPolicy(ctx, scope)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Attestation{}, err
	}
	if err := s.acceptPolicyVersion(cur, in.PolicyVersion, ""); err != nil {
		_ = scope.Rollback(ctx)
		return Attestation{}, err
	}
	pol, err := s.loadPolicyVersion(ctx, scope, in.PolicyVersion)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Attestation{}, err
	}
	agentName, ok := pol.Document.AgentNameForKey(in.KeyID)
	if !ok || !pol.Document.May("attest", agentName) {
		_ = scope.Rollback(ctx)
		return Attestation{}, ErrForbiddenByPolicy
	}
	if in.Status != "ok" && in.Status != "deny" {
		_ = scope.Rollback(ctx)
		return Attestation{}, errors.New("status must be ok or deny")
	}
	maxComment := pol.Document.Attestation.CommentMaxBytes
	if maxComment == 0 {
		maxComment = 4096
	}
	if len(in.Comment) > maxComment {
		_ = scope.Rollback(ctx)
		return Attestation{}, errors.New("comment too long")
	}
	msg, err := s.getMessage(ctx, scope, in.MessageID)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Attestation{}, err
	}
	createdAt := in.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	envelope := map[string]any{
		"messageId":     float64(in.MessageID),
		"messageHash":   msg.Hash,
		"policyVersion": float64(in.PolicyVersion),
		"signerKeyId":   in.KeyID,
		"status":        in.Status,
		"comment":       in.Comment,
		"createdAt":     createdAt.Format(time.RFC3339Nano),
	}
	sig, err := DecodeB64(in.Signature)
	if err != nil || !VerifyAgentSig(key.Algorithm, key.PublicKey, sig, envelope) {
		_ = scope.Rollback(ctx)
		return Attestation{}, ErrInvalidSignature
	}
	att := Attestation{
		ObjectID: scope.ObjectID(), MessageID: in.MessageID, SignerKeyID: in.KeyID,
		PolicyVersion: in.PolicyVersion, Status: in.Status, Comment: in.Comment,
		MessageHash: msg.Hash, Signature: in.Signature, CreatedAt: createdAt,
	}
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		now := createdAt
		for i := range s.memAttests[scope.ObjectID()] {
			a := &s.memAttests[scope.ObjectID()][i]
			if a.MessageID == in.MessageID && a.SignerKeyID == in.KeyID && a.SupersededAt == nil {
				a.SupersededAt = &now
			}
		}
		att.ID = int64(len(s.memAttests[scope.ObjectID()]) + 1)
		s.memAttests[scope.ObjectID()] = append(s.memAttests[scope.ObjectID()], att)
		eid := int64(len(s.memEvents[scope.ObjectID()]) + 1)
		aid := att.ID
		s.memEvents[scope.ObjectID()] = append(s.memEvents[scope.ObjectID()], Event{
			ID: eid, ObjectID: scope.ObjectID(), Kind: "attestation_added", MessageID: in.MessageID, AttestationID: &aid, CreatedAt: createdAt,
		})
		_ = scope.Commit(ctx)
		return att, nil
	}
	_, _ = scope.Tx().Exec(ctx, `UPDATE securebus_attestations SET superseded_at=$1
WHERE object_id=$2 AND message_id=$3 AND signer_key_id=$4 AND superseded_at IS NULL`, createdAt, scope.ObjectID(), in.MessageID, in.KeyID)
	msgHashBytes, _ := DecodeB64(msg.Hash)
	var id int64
	err = scope.Tx().QueryRow(ctx, `INSERT INTO securebus_attestations
(object_id, message_id, signer_key_id, policy_version, status, comment, message_hash, signature, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		scope.ObjectID(), in.MessageID, in.KeyID, in.PolicyVersion, in.Status, in.Comment, msgHashBytes, sig, createdAt).Scan(&id)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Attestation{}, err
	}
	att.ID = id
	_, err = scope.Tx().Exec(ctx, `INSERT INTO securebus_events (object_id, kind, message_id, attestation_id, created_at)
VALUES ($1,'attestation_added',$2,$3,$4)`, scope.ObjectID(), in.MessageID, id, createdAt)
	if err != nil {
		_ = scope.Rollback(ctx)
		return Attestation{}, err
	}
	return att, scope.Commit(ctx)
}

func (s *Service) getMessage(ctx context.Context, scope *policy.Scope, id int64) (Message, error) {
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, m := range s.memMessages[scope.ObjectID()] {
			if m.ID == id {
				return m, nil
			}
		}
		return Message{}, ErrNotFound
	}
	row := scope.Tx().QueryRow(ctx, `SELECT id, object_id, author_key_id, policy_version, policy_hash, nonce, payload::text, hash, prev_hash, signature, created_at
FROM securebus_messages WHERE object_id=$1 AND id=$2`, scope.ObjectID(), id)
	var m Message
	var payloadRaw string
	var hash, polHash, prev, sig []byte
	if err := row.Scan(&m.ID, &m.ObjectID, &m.AuthorKeyID, &m.PolicyVersion, &polHash, &m.Nonce, &payloadRaw, &hash, &prev, &sig, &m.CreatedAt); err != nil {
		return Message{}, ErrNotFound
	}
	_ = json.Unmarshal([]byte(payloadRaw), &m.Payload)
	m.Hash = B64(hash)
	m.PolicyHash = B64(polHash)
	if len(prev) > 0 {
		m.PrevHash = B64(prev)
	}
	m.Signature = B64(sig)
	return m, nil
}

type VerifyResult struct {
	Message      Message              `json:"message"`
	Attestations []Attestation        `json:"attestations"`
	SignatureOK  bool                 `json:"signatureOk"`
	HashOK       bool                 `json:"hashOk"`
	PolicyVersion int                 `json:"policyVersion"`
	AttestationChecks []map[string]any `json:"attestationChecks"`
}

func (s *Service) Verify(ctx context.Context, scope *policy.Scope, messageID int64) (VerifyResult, error) {
	if scope == nil || scope.Kind() != models.ObjectKindSecureBus {
		return VerifyResult{}, policy.ErrKindMismatch
	}
	msg, err := s.getMessage(ctx, scope, messageID)
	if err != nil {
		_ = scope.Rollback(ctx)
		return VerifyResult{}, err
	}
	atts, err := s.listAttestations(ctx, scope, messageID)
	if err != nil {
		_ = scope.Rollback(ctx)
		return VerifyResult{}, err
	}
	key, ok, _ := s.keys.Get(ctx, msg.AuthorKeyID)
	envelope := map[string]any{
		"objectId": scope.ObjectID(), "policyVersion": float64(msg.PolicyVersion),
		"policyHash": msg.PolicyHash, "authorKeyId": msg.AuthorKeyID, "nonce": msg.Nonce,
		"createdAt": msg.CreatedAt.Format(time.RFC3339Nano), "payload": msg.Payload,
	}
	if msg.PrevHash != "" {
		envelope["prevHash"] = msg.PrevHash
	}
	hash, _ := HashCanonical(envelope)
	hashOK := B64(hash) == msg.Hash
	sigOK := false
	if ok {
		sig, err := DecodeB64(msg.Signature)
		sigOK = err == nil && VerifyAgentSig(key.Algorithm, key.PublicKey, sig, envelope)
	}
	checks := []map[string]any{}
	for _, a := range atts {
		ak, aok, _ := s.keys.Get(ctx, a.SignerKeyID)
		env := map[string]any{
			"messageId": float64(a.MessageID), "messageHash": a.MessageHash,
			"policyVersion": float64(a.PolicyVersion), "signerKeyId": a.SignerKeyID,
			"status": a.Status, "comment": a.Comment, "createdAt": a.CreatedAt.Format(time.RFC3339Nano),
		}
		asig, err := DecodeB64(a.Signature)
		aokSig := aok && err == nil && VerifyAgentSig(ak.Algorithm, ak.PublicKey, asig, env)
		checks = append(checks, map[string]any{
			"signerKeyId": a.SignerKeyID, "status": a.Status, "signatureOk": aokSig,
			"keyRevokedAt": ak.RevokedAt, "superseded": a.SupersededAt != nil,
		})
	}
	_ = scope.Commit(ctx)
	return VerifyResult{
		Message: msg, Attestations: atts, SignatureOK: sigOK, HashOK: hashOK,
		PolicyVersion: msg.PolicyVersion, AttestationChecks: checks,
	}, nil
}

func (s *Service) listAttestations(ctx context.Context, scope *policy.Scope, messageID int64) ([]Attestation, error) {
	if scope.Memory() || scope.Tx() == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := []Attestation{}
		for _, a := range s.memAttests[scope.ObjectID()] {
			if a.MessageID == messageID {
				out = append(out, a)
			}
		}
		return out, nil
	}
	rows, err := scope.Tx().Query(ctx, `SELECT id, object_id, message_id, signer_key_id, policy_version, status, comment, message_hash, signature, created_at, superseded_at
FROM securebus_attestations WHERE object_id=$1 AND message_id=$2 ORDER BY id`, scope.ObjectID(), messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attestation{}
	for rows.Next() {
		var a Attestation
		var mh, sig []byte
		if err := rows.Scan(&a.ID, &a.ObjectID, &a.MessageID, &a.SignerKeyID, &a.PolicyVersion, &a.Status, &a.Comment, &mh, &sig, &a.CreatedAt, &a.SupersededAt); err != nil {
			return nil, err
		}
		a.MessageHash = B64(mh)
		a.Signature = B64(sig)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) Keys(ctx context.Context, scope *policy.Scope) ([]agentkeys.Key, error) {
	if scope == nil || scope.Kind() != models.ObjectKindSecureBus {
		return nil, policy.ErrKindMismatch
	}
	pol, err := s.currentPolicy(ctx, scope)
	if err != nil {
		_ = scope.Commit(ctx)
		return nil, err
	}
	out := []agentkeys.Key{}
	for _, a := range pol.Document.Agents {
		k, ok, err := s.keys.Get(ctx, a.KeyID)
		if err != nil {
			_ = scope.Rollback(ctx)
			return nil, err
		}
		if ok {
			out = append(out, k)
		}
	}
	_ = scope.Commit(ctx)
	return out, nil
}
