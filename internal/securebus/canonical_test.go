package securebus

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestCanonicalJSONStable(t *testing.T) {
	a := map[string]any{"b": 1, "a": map[string]any{"z": true, "y": "x"}}
	b := map[string]any{"a": map[string]any{"y": "x", "z": true}, "b": 1}
	ca, err := CanonicalJSON(a)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := CanonicalJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(ca) != string(cb) {
		t.Fatalf("not stable:\n%s\n%s", ca, cb)
	}
	if string(ca) != `{"a":{"y":"x","z":true},"b":1}` {
		t.Fatalf("got %s", ca)
	}
}

func TestSignVerifyRoundtrip(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]any{
		"objectId": "obj1", "nonce": "n1", "payload": map[string]any{"x": 1},
	}
	sig, err := SignEd25519(priv, env)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyEd25519(priv.Public().(ed25519.PublicKey), env, sig) {
		t.Fatal("verify failed")
	}
	env["nonce"] = "n2"
	if VerifyEd25519(priv.Public().(ed25519.PublicKey), env, sig) {
		t.Fatal("tampered envelope should fail")
	}
}
