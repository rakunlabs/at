package crypto

import (
	"fmt"

	"github.com/rakunlabs/at/internal/config"
)

// EncryptLLMConfig encrypts the sensitive fields of an LLMConfig (api_key and
// extra_headers values) in-place and returns the modified config.
// If key is nil, the config is returned unchanged (no-op).
func EncryptLLMConfig(cfg config.LLMConfig, key []byte) (config.LLMConfig, error) {
	if key == nil {
		return cfg, nil
	}

	if cfg.APIKey != "" {
		enc, err := Encrypt(cfg.APIKey, key)
		if err != nil {
			return cfg, fmt.Errorf("encrypt api_key: %w", err)
		}
		cfg.APIKey = enc
	}

	if len(cfg.ExtraHeaders) > 0 {
		encrypted := make(map[string]string, len(cfg.ExtraHeaders))
		for k, v := range cfg.ExtraHeaders {
			enc, err := Encrypt(v, key)
			if err != nil {
				return cfg, fmt.Errorf("encrypt extra_header %q: %w", k, err)
			}
			encrypted[k] = enc
		}
		cfg.ExtraHeaders = encrypted
	}

	return cfg, nil
}

// DecryptLLMConfig decrypts the sensitive fields of an LLMConfig (api_key and
// extra_headers values) in-place and returns the modified config.
// If key is nil, the config is returned unchanged (no-op).
// Values that are not encrypted (no "enc:" prefix) are left as-is.
func DecryptLLMConfig(cfg config.LLMConfig, key []byte) (config.LLMConfig, error) {
	if key == nil {
		return cfg, nil
	}

	if cfg.APIKey != "" {
		dec, err := Decrypt(cfg.APIKey, key)
		if err != nil {
			return cfg, fmt.Errorf("decrypt api_key: %w", err)
		}
		cfg.APIKey = dec
	}

	if len(cfg.ExtraHeaders) > 0 {
		decrypted := make(map[string]string, len(cfg.ExtraHeaders))
		for k, v := range cfg.ExtraHeaders {
			dec, err := Decrypt(v, key)
			if err != nil {
				return cfg, fmt.Errorf("decrypt extra_header %q: %w", k, err)
			}
			decrypted[k] = dec
		}
		cfg.ExtraHeaders = decrypted
	}

	return cfg, nil
}
