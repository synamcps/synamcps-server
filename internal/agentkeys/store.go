package agentkeys

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Key struct {
	KeyID      string     `json:"keyId"`
	SubjectKey string     `json:"subjectKey"`
	TokenID    string     `json:"tokenId,omitempty"`
	Algorithm  string     `json:"alg"`
	PublicKey  []byte     `json:"-"`
	PublicKeyB64 string   `json:"publicKey"`
	CreatedAt  time.Time  `json:"createdAt"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

type Challenge struct {
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expiresAt"`
	Binder    string    `json:"-"`
}

type Store struct {
	mu     sync.Mutex
	mem    map[string]Key
	challs map[string]Challenge
	pool   *pgxpool.Pool
	redis  *redis.Client
	prefix string
}

func NewStore(pool *pgxpool.Pool, rdb *redis.Client, keyPrefix string) *Store {
	return &Store{
		mem:    map[string]Key{},
		challs: map[string]Challenge{},
		pool:   pool,
		redis:  rdb,
		prefix: keyPrefix,
	}
}

func (s *Store) CreateChallenge(ctx context.Context, binder string, ttl time.Duration) (Challenge, error) {
	if binder == "" {
		return Challenge{}, errors.New("binder required")
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	ch := Challenge{
		Nonce:     uuid.NewString(),
		ExpiresAt: time.Now().UTC().Add(ttl),
		Binder:    binder,
	}
	if s.redis != nil {
		key := s.prefix + ":agentkey:chal:" + ch.Nonce
		payload := binder + "|" + ch.ExpiresAt.Format(time.RFC3339Nano)
		if err := s.redis.Set(ctx, key, payload, ttl).Err(); err != nil {
			return Challenge{}, err
		}
		return ch, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.challs[ch.Nonce] = ch
	return ch, nil
}

func (s *Store) ConsumeChallenge(ctx context.Context, nonce, binder string) error {
	if nonce == "" {
		return errors.New("nonce required")
	}
	if s.redis != nil {
		key := s.prefix + ":agentkey:chal:" + nonce
		val, err := s.redis.GetDel(ctx, key).Result()
		if err == redis.Nil {
			return errors.New("challenge not found or expired")
		}
		if err != nil {
			return err
		}
		storedBinder := val
		if i := indexByte(val, '|'); i >= 0 {
			storedBinder = val[:i]
		}
		if storedBinder != binder {
			return errors.New("challenge binder mismatch")
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.challs[nonce]
	if !ok || time.Now().After(ch.ExpiresAt) {
		delete(s.challs, nonce)
		return errors.New("challenge not found or expired")
	}
	if ch.Binder != binder {
		return errors.New("challenge binder mismatch")
	}
	delete(s.challs, nonce)
	return nil
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

type RegisterInput struct {
	SubjectKey string
	TokenID    string
	Algorithm  string
	PublicKey  []byte
	Nonce      string
	Signature  []byte
	Binder     string
}

func (s *Store) Register(ctx context.Context, in RegisterInput) (Key, error) {
	if in.SubjectKey == "" || len(in.PublicKey) == 0 || in.Nonce == "" || len(in.Signature) == 0 {
		return Key{}, errors.New("subjectKey, publicKey, nonce, and signature required")
	}
	alg := in.Algorithm
	if alg == "" {
		alg = "ed25519"
	}
	switch alg {
	case "ed25519":
		if len(in.PublicKey) != ed25519.PublicKeySize {
			return Key{}, errors.New("invalid ed25519 public key length")
		}
	case "ecdsa-p256":
		if _, err := parseECDSAP256(in.PublicKey); err != nil {
			return Key{}, err
		}
	default:
		return Key{}, fmt.Errorf("unsupported algorithm %q", alg)
	}
	if err := s.ConsumeChallenge(ctx, in.Nonce, in.Binder); err != nil {
		return Key{}, err
	}
	envelope := map[string]any{
		"nonce":     in.Nonce,
		"publicKey": base64.RawURLEncoding.EncodeToString(in.PublicKey),
	}
	if in.TokenID != "" {
		envelope["tokenId"] = in.TokenID
	} else {
		envelope["sessionId"] = in.Binder
	}
	ok := false
	switch alg {
	case "ed25519":
		ok = verifyEd25519(in.PublicKey, envelope, in.Signature)
	case "ecdsa-p256":
		ok = verifyECDSAP256(in.PublicKey, envelope, in.Signature)
	}
	if !ok {
		return Key{}, errors.New("invalid proof-of-possession signature")
	}
	now := time.Now().UTC()
	key := Key{
		KeyID:        "k_" + uuid.NewString(),
		SubjectKey:   in.SubjectKey,
		TokenID:      in.TokenID,
		Algorithm:    alg,
		PublicKey:    append([]byte{}, in.PublicKey...),
		PublicKeyB64: base64.RawURLEncoding.EncodeToString(in.PublicKey),
		CreatedAt:    now,
	}
	if s.pool == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.mem[key.KeyID] = key
		return key, nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO agent_keys (key_id, subject_key, token_id, algorithm, public_key, created_at)
VALUES ($1,$2,$3,$4,$5,$6)`, key.KeyID, key.SubjectKey, nullIfEmpty(key.TokenID), key.Algorithm, key.PublicKey, key.CreatedAt)
	if err != nil {
		return Key{}, err
	}
	return key, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func verifyEd25519(pub []byte, envelope map[string]any, sig []byte) bool {
	raw, err := canonicalJSON(envelope)
	if err != nil {
		return false
	}
	sum := sha256Sum(raw)
	return ed25519.Verify(ed25519.PublicKey(pub), sum, sig)
}

func parseECDSAP256(pub []byte) (*ecdsa.PublicKey, error) {
	x, y := elliptic.Unmarshal(elliptic.P256(), pub)
	if x == nil {
		return nil, errors.New("invalid ecdsa-p256 public key")
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
}

func verifyECDSAP256(pub []byte, envelope map[string]any, sig []byte) bool {
	pk, err := parseECDSAP256(pub)
	if err != nil {
		return false
	}
	raw, err := canonicalJSON(envelope)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(raw)
	// WebCrypto produces IEEE P1363 (r||s) or ASN.1; try both.
	if ecdsa.VerifyASN1(pk, sum[:], sig) {
		return true
	}
	if len(sig) == 64 {
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		return ecdsa.Verify(pk, sum[:], r, s)
	}
	return false
}

func (s *Store) Get(ctx context.Context, keyID string) (Key, bool, error) {
	if s.pool == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		k, ok := s.mem[keyID]
		return k, ok, nil
	}
	row := s.pool.QueryRow(ctx, `SELECT key_id, subject_key, COALESCE(token_id,''), algorithm, public_key, created_at, revoked_at
FROM agent_keys WHERE key_id=$1`, keyID)
	var k Key
	var revoked *time.Time
	if err := row.Scan(&k.KeyID, &k.SubjectKey, &k.TokenID, &k.Algorithm, &k.PublicKey, &k.CreatedAt, &revoked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Key{}, false, nil
		}
		return Key{}, false, err
	}
	k.PublicKeyB64 = base64.RawURLEncoding.EncodeToString(k.PublicKey)
	k.RevokedAt = revoked
	return k, true, nil
}

func (s *Store) ListForSubject(ctx context.Context, subjectKey string) ([]Key, error) {
	if s.pool == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := []Key{}
		for _, k := range s.mem {
			if k.SubjectKey == subjectKey {
				out = append(out, k)
			}
		}
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT key_id, subject_key, COALESCE(token_id,''), algorithm, public_key, created_at, revoked_at
FROM agent_keys WHERE subject_key=$1 ORDER BY created_at DESC`, subjectKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		var revoked *time.Time
		if err := rows.Scan(&k.KeyID, &k.SubjectKey, &k.TokenID, &k.Algorithm, &k.PublicKey, &k.CreatedAt, &revoked); err != nil {
			return nil, err
		}
		k.PublicKeyB64 = base64.RawURLEncoding.EncodeToString(k.PublicKey)
		k.RevokedAt = revoked
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) Revoke(ctx context.Context, keyID, subjectKey string) error {
	now := time.Now().UTC()
	if s.pool == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		k, ok := s.mem[keyID]
		if !ok || k.SubjectKey != subjectKey {
			return errors.New("key not found")
		}
		k.RevokedAt = &now
		s.mem[keyID] = k
		return nil
	}
	tag, err := s.pool.Exec(ctx, `UPDATE agent_keys SET revoked_at=$1 WHERE key_id=$2 AND subject_key=$3 AND revoked_at IS NULL`, now, keyID, subjectKey)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("key not found")
	}
	return nil
}

// BelongsToCaller checks key ownership: same subject, and if key has token_id it must match.
func (k Key) BelongsToCaller(subjectKey, tokenID string) bool {
	if k.SubjectKey != subjectKey {
		return false
	}
	if k.RevokedAt != nil {
		return false
	}
	if k.TokenID != "" && tokenID != "" && k.TokenID != tokenID {
		return false
	}
	if k.TokenID != "" && tokenID == "" {
		return false
	}
	return true
}
