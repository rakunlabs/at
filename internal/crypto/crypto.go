// Package crypto provides AES-256-GCM encryption for sensitive provider
// configuration fields (API keys, header values) stored in the database.
//
// Encrypted values are prefixed with "enc:" followed by base64-encoded
// ciphertext (nonce + sealed data). This makes it trivial to distinguish
// encrypted values from legacy plaintext on read.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encPrefix = "enc:"

// Encrypt encrypts plaintext using AES-256-GCM and returns a string with
// the format "enc:<base64(nonce + ciphertext)>".
// The key must be exactly 32 bytes (256 bits).
// Returns the original string unchanged if it is empty.
func Encrypt(plaintext string, key []byte) (string, error) {
	if plaintext == "" {
		return plaintext, nil
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	// Seal appends the ciphertext to nonce, giving us nonce+ciphertext in one slice.
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	return encPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt decrypts a value previously produced by Encrypt.
// If the value does not start with "enc:", it is returned as-is (plaintext passthrough).
// The key must be exactly 32 bytes (256 bits).
func Decrypt(ciphertext string, key []byte) (string, error) {
	if !IsEncrypted(ciphertext) {
		return ciphertext, nil
	}

	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, encPrefix))
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, sealed := data[:nonceSize], data[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	return string(plaintext), nil
}

// IsEncrypted reports whether the value carries the "enc:" prefix,
// meaning it was produced by Encrypt.
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, encPrefix)
}

// DeriveKey derives a 32-byte AES-256 key from an arbitrary-length
// passphrase by hashing it with SHA-256. Any non-empty string works,
// including short values like "test".
//
// Returns an error if the input is empty.
func DeriveKey(passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("encryption key must not be empty")
	}

	hash := sha256.Sum256([]byte(passphrase))

	return hash[:], nil
}
