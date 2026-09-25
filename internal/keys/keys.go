// Package keys generates API keys and webhook signing secrets.
package keys

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

const apiKeyPrefix = "hr_"

// NewAPIKey returns the full key (shown to the user exactly once), a short
// display prefix, and the SHA-256 hash that is stored in the database.
//
// Why SHA-256 and not bcrypt? bcrypt is slow on purpose, to protect
// low-entropy human passwords. These keys are 32 random bytes, so they can't
// be brute-forced, and a fast deterministic hash lets us find the key with
// a single indexed lookup instead of comparing against every row.
func NewAPIKey() (full, prefix string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", nil, err
	}
	full = apiKeyPrefix + hex.EncodeToString(b)
	prefix = full[:len(apiKeyPrefix)+8]
	return full, prefix, HashAPIKey(full), nil
}

// HashAPIKey returns the value stored in api_keys.key_hash.
func HashAPIKey(full string) []byte {
	sum := sha256.Sum256([]byte(full))
	return sum[:]
}

// NewSigningSecret returns a secret in the Standard Webhooks format:
// "whsec_" + base64(32 random bytes).
func NewSigningSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(b), nil
}
