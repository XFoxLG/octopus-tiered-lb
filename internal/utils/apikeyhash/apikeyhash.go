// Package apikeyhash provides the deterministic SHA-256 hash of a plaintext
// API key, used for the api_keys.api_key_hash lookup column.
//
// The stored api_key column carries AES-GCM ciphertext (enc: prefix) whose
// random nonce makes it unusable in WHERE clauses, so a deterministic hash
// column is kept alongside it. This logic is shared by op/apikey (CRUD and
// cache refresh), op/backup (import re-encryption) and db/migrate (backfill)
// — keep it here so the three callers cannot drift apart.
package apikeyhash

import (
	"crypto/sha256"
	"encoding/hex"
)

// Sum returns the lowercase hex SHA-256 of a plaintext API key.
func Sum(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
