package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
)

const encryptedPrefix = "enc:"

var (
	globalKey     []byte
	globalKeyOnce sync.Once

	ErrNoKey          = errors.New("encryption key not configured")
	ErrDecryptFailed  = errors.New("decryption failed")
	ErrInvalidPayload = errors.New("invalid encrypted payload")
)

// Init derives a 256-bit AES key from the raw secret and stores it for the
// process lifetime. Call once at startup; subsequent calls are no-ops.
func Init(rawSecret string) {
	globalKeyOnce.Do(func() {
		h := sha256.Sum256([]byte(rawSecret))
		globalKey = h[:]
	})
}

// Encrypt returns the AES-GCM ciphertext as a base64 string prefixed with
// "enc:". An empty plaintext is returned as-is. If the key was never
// initialized (crypto.Init not called) and the plaintext is non-empty, Encrypt
// returns ErrNoKey rather than silently storing the value in plaintext — this
// prevents encrypted fields (tokens, passwords, API keys) from being persisted
// unencrypted when the process started without a configured key.
func Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return plaintext, nil
	}
	if globalKey == nil {
		return "", ErrNoKey
	}
	block, err := aes.NewCipher(globalKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encryptedPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. Values without the "enc:" prefix are returned
// as-is (legacy unencrypted data).
func Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" || !strings.HasPrefix(ciphertext, encryptedPrefix) {
		return ciphertext, nil
	}
	if globalKey == nil {
		return "", ErrNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, encryptedPrefix))
	if err != nil {
		return "", ErrInvalidPayload
	}
	block, err := aes.NewCipher(globalKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", ErrInvalidPayload
	}
	plaintext, err := gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", ErrDecryptFailed
	}
	return string(plaintext), nil
}

// IsEncrypted reports whether the value carries the "enc:" prefix.
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, encryptedPrefix)
}

// ResetForTest clears the initialized key and the sync.Once guard so a test
// can call Init with a different secret. Production code must never call
// this: rotating the process key would orphan previously encrypted data.
func ResetForTest() {
	globalKey = nil
	globalKeyOnce = sync.Once{}
}
