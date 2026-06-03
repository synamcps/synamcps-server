package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrInvalidKey   = errors.New("secrets key must be 32 bytes")
	ErrCipherNotSet = errors.New("secrets cipher is not configured")
)

type Cipher struct {
	gcm cipher.AEAD
}

func NewCipher(keyMaterial string) (*Cipher, error) {
	key, err := decodeKey(keyMaterial)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{gcm: gcm}, nil
}

func decodeKey(material string) ([]byte, error) {
	material = strings.TrimSpace(material)
	if material == "" {
		return nil, ErrCipherNotSet
	}
	if raw, err := base64.StdEncoding.DecodeString(material); err == nil && len(raw) == 32 {
		return raw, nil
	}
	if raw, err := base64.RawStdEncoding.DecodeString(material); err == nil && len(raw) == 32 {
		return raw, nil
	}
	if raw, err := hex.DecodeString(material); err == nil && len(raw) == 32 {
		return raw, nil
	}
	if len(material) == 32 {
		return []byte(material), nil
	}
	return nil, ErrInvalidKey
}

func (c *Cipher) Encrypt(plaintext string) (ciphertext, nonce []byte, err error) {
	if c == nil || c.gcm == nil {
		return nil, nil, ErrCipherNotSet
	}
	if plaintext == "" {
		return nil, nil, nil
	}
	nonce = make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("nonce: %w", err)
	}
	return c.gcm.Seal(nil, nonce, []byte(plaintext), nil), nonce, nil
}

func (c *Cipher) Decrypt(ciphertext, nonce []byte) (string, error) {
	if c == nil || c.gcm == nil {
		return "", ErrCipherNotSet
	}
	if len(ciphertext) == 0 {
		return "", nil
	}
	raw, err := c.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(raw), nil
}
