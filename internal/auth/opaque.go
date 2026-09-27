package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// RandomOpaqueToken returns a cryptographically random, hex-encoded token.
func RandomOpaqueToken(byteCount int) (string, error) {
	if byteCount <= 0 {
		return "", fmt.Errorf("invalid random token length")
	}
	buf := make([]byte, byteCount)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// OpaqueSecretHash returns the digest persisted for a one-time opaque secret.
func OpaqueSecretHash(secret string) [sha256.Size]byte {
	return sha256.Sum256([]byte(secret))
}

// OpaqueSecretMatches compares a plaintext secret with a stored SHA-256 digest
// without data-dependent comparison timing.
func OpaqueSecretMatches(secret string, stored []byte) bool {
	digest := OpaqueSecretHash(secret)
	return len(stored) == sha256.Size && subtle.ConstantTimeCompare(digest[:], stored) == 1
}
