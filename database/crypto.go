package database

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// encKey is the static AES-256 key used to encrypt sensitive fields
// (phone numbers, session strings) before they are written to MongoDB.
//
// This is obfuscation against a raw DB leak (e.g. a leaked MongoDB URI),
// NOT defense against someone who also has this source code — the key is
// embedded in the binary. Anyone with both the DB dump and this key can
// still decrypt. The goal is: a bare DB leak alone is useless.
//
// 32 bytes -> AES-256.
var encKey = []byte("c7f1b9a3e6d4082f5a91c3b7e0d2f4a8")

// encryptStr encrypts raw with AES-GCM and returns a compact base64 string
// of (nonce || ciphertext || tag). GCM keeps the overhead small: 12 byte
// nonce + 16 byte auth tag of fixed cost, no padding — important to keep
// MongoDB storage growth minimal on free-tier quotas.
func encryptStr(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return "", fmt.Errorf("encryptStr: new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("encryptStr: new gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("encryptStr: nonce: %w", err)
	}

	sealed := gcm.Seal(nonce, nonce, []byte(raw), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptStr reverses encryptStr.
func decryptStr(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}

	data, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("decryptStr: base64: %w", err)
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return "", fmt.Errorf("decryptStr: new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("decryptStr: new gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("decryptStr: ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryptStr: %w", err)
	}
	return string(plain), nil
}
