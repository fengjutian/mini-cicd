package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

func NewSessionToken() (raw string, hash []byte, err error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", nil, fmt.Errorf("generate session token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(value)
	digest := sha256.Sum256([]byte(raw))
	return raw, digest[:], nil
}

func HashSessionToken(raw string) []byte {
	digest := sha256.Sum256([]byte(raw))
	return digest[:]
}
