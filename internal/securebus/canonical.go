package securebus

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
)

// CanonicalJSON produces RFC 8785-style deterministic JSON: object keys sorted
// recursively, no insignificant whitespace. Sufficient for controlled envelopes.
func CanonicalJSON(v any) ([]byte, error) {
	norm, err := normalize(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(norm)
}

func normalize(v any) (any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case bool, float64, string:
		return t, nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return nil, err
		}
		return f, nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			n, err := normalize(t[k])
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			n, err := normalize(item)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		var decoded any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&decoded); err != nil {
			return nil, err
		}
		return normalize(decoded)
	}
}

// HashCanonical returns SHA-256 of the canonical JSON of v.
func HashCanonical(v any) ([]byte, error) {
	raw, err := CanonicalJSON(v)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}

// SignEd25519 signs the SHA-256 of canonical JSON with an Ed25519 private key.
func SignEd25519(priv ed25519.PrivateKey, envelope any) ([]byte, error) {
	h, err := HashCanonical(envelope)
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(priv, h), nil
}

// VerifyEd25519 verifies an Ed25519 signature over the SHA-256 of canonical JSON.
func VerifyEd25519(pub ed25519.PublicKey, envelope any, sig []byte) bool {
	h, err := HashCanonical(envelope)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, h, sig)
}

// VerifyAgentSig verifies ed25519 or ecdsa-p256 over HashCanonical(envelope).
func VerifyAgentSig(alg string, pub, sig []byte, envelope any) bool {
	switch alg {
	case "", "ed25519":
		return VerifyEd25519(ed25519.PublicKey(pub), envelope, sig)
	case "ecdsa-p256":
		x, y := elliptic.Unmarshal(elliptic.P256(), pub)
		if x == nil {
			return false
		}
		pk := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
		h, err := HashCanonical(envelope)
		if err != nil {
			return false
		}
		if ecdsa.VerifyASN1(pk, h, sig) {
			return true
		}
		if len(sig) == 64 {
			r := new(big.Int).SetBytes(sig[:32])
			sVal := new(big.Int).SetBytes(sig[32:])
			return ecdsa.Verify(pk, h, r, sVal)
		}
		return false
	default:
		return false
	}
}

func B64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func DecodeB64(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("empty encoding")
	}
	if out, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return out, nil
	}
	return base64.StdEncoding.DecodeString(s)
}
