package crypto

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

func testKey() []byte {
	key, _ := DeriveKey("test-encryption-key-for-unit-tests")
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey()
	original := "sk-ant-api03-secret-key-value"

	encrypted, err := Encrypt(original, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if !IsEncrypted(encrypted) {
		t.Fatalf("expected encrypted value to start with %q prefix, got %q", "enc:", encrypted)
	}

	if encrypted == original {
		t.Fatal("encrypted value should differ from plaintext")
	}

	decrypted, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if decrypted != original {
		t.Fatalf("round-trip failed: got %q, want %q", decrypted, original)
	}
}

func TestEncryptEmptyString(t *testing.T) {
	key := testKey()

	encrypted, err := Encrypt("", key)
	if err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}

	if encrypted != "" {
		t.Fatalf("encrypting empty string should return empty, got %q", encrypted)
	}
}

func TestDecryptPlaintextPassthrough(t *testing.T) {
	key := testKey()

	// A value without the "enc:" prefix should be returned as-is.
	plain := "sk-plain-api-key"
	result, err := Decrypt(plain, key)
	if err != nil {
		t.Fatalf("Decrypt plaintext: %v", err)
	}

	if result != plain {
		t.Fatalf("plaintext passthrough failed: got %q, want %q", result, plain)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := testKey()
	key2, _ := DeriveKey("different-key-entirely")

	encrypted, err := Encrypt("secret", key1)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err = Decrypt(encrypted, key2)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestIsEncrypted(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"enc:abc123", true},
		{"enc:", true},
		{"ENC:abc", false},
		{"plaintext", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := IsEncrypted(tt.value); got != tt.want {
			t.Errorf("IsEncrypted(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestDeriveKey(t *testing.T) {
	key, err := DeriveKey("short")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("key length = %d, want 32", len(key))
	}

	// Long passphrase should still produce a 32-byte key.
	longKey, err := DeriveKey(strings.Repeat("a", 100))
	if err != nil {
		t.Fatalf("DeriveKey long: %v", err)
	}
	if len(longKey) != 32 {
		t.Fatalf("long key length = %d, want 32", len(longKey))
	}

	// Different passphrases should produce different keys.
	key2, _ := DeriveKey("different")
	if string(key) == string(key2) {
		t.Fatal("different passphrases should produce different keys")
	}

	// Empty passphrase should error.
	_, err = DeriveKey("")
	if err == nil {
		t.Fatal("expected error for empty passphrase")
	}
}

func TestEncryptUniqueNonces(t *testing.T) {
	key := testKey()
	plain := "same-plaintext"

	enc1, _ := Encrypt(plain, key)
	enc2, _ := Encrypt(plain, key)

	if enc1 == enc2 {
		t.Fatal("two encryptions of the same plaintext should produce different ciphertext (unique nonces)")
	}

	// Both should decrypt to the same value.
	dec1, _ := Decrypt(enc1, key)
	dec2, _ := Decrypt(enc2, key)

	if dec1 != plain || dec2 != plain {
		t.Fatalf("both should decrypt to %q, got %q and %q", plain, dec1, dec2)
	}
}

// ─── LLMConfig helpers ───

func TestEncryptDecryptLLMConfig(t *testing.T) {
	key := testKey()

	original := config.LLMConfig{
		Type:   "openai",
		APIKey: "sk-secret-key",
		ExtraHeaders: map[string]string{
			"X-Custom-Auth": "bearer-token-123",
			"Accept":        "application/json",
		},
		BaseURL: "https://api.openai.com/v1/chat/completions",
		Model:   "gpt-4o",
	}

	encrypted, err := EncryptLLMConfig(original, key)
	if err != nil {
		t.Fatalf("EncryptLLMConfig: %v", err)
	}

	// Sensitive fields should be encrypted.
	if !IsEncrypted(encrypted.APIKey) {
		t.Fatalf("api_key should be encrypted, got %q", encrypted.APIKey)
	}
	for k, v := range encrypted.ExtraHeaders {
		if !IsEncrypted(v) {
			t.Fatalf("extra_header %q should be encrypted, got %q", k, v)
		}
	}

	// Non-sensitive fields should be unchanged.
	if encrypted.Type != original.Type {
		t.Fatalf("type changed: got %q, want %q", encrypted.Type, original.Type)
	}
	if encrypted.BaseURL != original.BaseURL {
		t.Fatalf("base_url changed: got %q, want %q", encrypted.BaseURL, original.BaseURL)
	}
	if encrypted.Model != original.Model {
		t.Fatalf("model changed: got %q, want %q", encrypted.Model, original.Model)
	}

	// Round-trip: decrypt back.
	decrypted, err := DecryptLLMConfig(encrypted, key)
	if err != nil {
		t.Fatalf("DecryptLLMConfig: %v", err)
	}

	if decrypted.APIKey != original.APIKey {
		t.Fatalf("api_key round-trip: got %q, want %q", decrypted.APIKey, original.APIKey)
	}
	for k, v := range original.ExtraHeaders {
		if decrypted.ExtraHeaders[k] != v {
			t.Fatalf("extra_header %q round-trip: got %q, want %q", k, decrypted.ExtraHeaders[k], v)
		}
	}
}

func TestEncryptDecryptLLMConfigNilKey(t *testing.T) {
	original := config.LLMConfig{
		Type:   "openai",
		APIKey: "sk-plaintext",
		ExtraHeaders: map[string]string{
			"X-Key": "value",
		},
	}

	// With nil key, should be a no-op.
	result, err := EncryptLLMConfig(original, nil)
	if err != nil {
		t.Fatalf("EncryptLLMConfig nil key: %v", err)
	}
	if result.APIKey != original.APIKey {
		t.Fatalf("nil key should not change api_key: got %q", result.APIKey)
	}

	result, err = DecryptLLMConfig(original, nil)
	if err != nil {
		t.Fatalf("DecryptLLMConfig nil key: %v", err)
	}
	if result.APIKey != original.APIKey {
		t.Fatalf("nil key should not change api_key: got %q", result.APIKey)
	}
}
