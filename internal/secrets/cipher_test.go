package secrets

import (
	"encoding/base64"
	"testing"
)

func TestCipherRoundtrip(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, err := c.Encrypt("super-secret-token")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decrypt(ct, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if got != "super-secret-token" {
		t.Fatalf("got %q", got)
	}
}
