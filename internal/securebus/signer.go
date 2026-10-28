package securebus

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// PolicySigner holds the server Ed25519 key used only to sign policy documents.
type PolicySigner struct {
	KID  string
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

// NewPolicySignerFromMaterial accepts raw 32-byte seed (hex or base64) or a 64-byte private key.
// Empty material generates an ephemeral key (dev only).
func NewPolicySignerFromMaterial(material string) (*PolicySigner, error) {
	material = strings.TrimSpace(material)
	var seed []byte
	var err error
	if material == "" {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		return &PolicySigner{
			KID:  "syna-policy-" + B64(priv.Public().(ed25519.PublicKey)[:8]),
			Priv: priv,
			Pub:  priv.Public().(ed25519.PublicKey),
		}, nil
	}
	if seed, err = hex.DecodeString(material); err != nil {
		seed, err = base64.StdEncoding.DecodeString(material)
		if err != nil {
			seed, err = base64.RawURLEncoding.DecodeString(material)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("decode policy signing key: %w", err)
	}
	var priv ed25519.PrivateKey
	switch len(seed) {
	case ed25519.SeedSize:
		priv = ed25519.NewKeyFromSeed(seed)
	case ed25519.PrivateKeySize:
		priv = ed25519.PrivateKey(seed)
	default:
		return nil, errors.New("policy signing key must be 32-byte seed or 64-byte private key")
	}
	pub := priv.Public().(ed25519.PublicKey)
	return &PolicySigner{
		KID:  "syna-policy-" + B64(pub[:8]),
		Priv: priv,
		Pub:  pub,
	}, nil
}

func (s *PolicySigner) SignDocument(doc Document) (hash, sig []byte, err error) {
	if s == nil || len(s.Priv) == 0 {
		return nil, nil, errors.New("policy signer not configured")
	}
	hash, err = HashCanonical(doc.AsMap())
	if err != nil {
		return nil, nil, err
	}
	sig = ed25519.Sign(s.Priv, hash)
	return hash, sig, nil
}

func (s *PolicySigner) JWKS() map[string]any {
	if s == nil {
		return map[string]any{"keys": []any{}}
	}
	return map[string]any{
		"keys": []map[string]any{{
			"kty": "OKP",
			"crv": "Ed25519",
			"kid": s.KID,
			"alg": "EdDSA",
			"use": "sig",
			"x":   base64.RawURLEncoding.EncodeToString(s.Pub),
		}},
	}
}
