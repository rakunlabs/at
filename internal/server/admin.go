package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

// ─── Key Rotation API ───

type rotateKeyRequest struct {
	// EncryptionKey is the new encryption passphrase.
	// If empty, encryption is disabled and all credentials are stored as plaintext.
	EncryptionKey string `json:"encryption_key"`
}

// RotateKeyAPI handles POST /api/v1/settings/rotate-key.
// It re-encrypts all provider credentials with a new key.
// When clustering is enabled, it acquires a distributed lock and broadcasts
// the new key to all peers after the DB transaction commits.
func (s *Server) RotateKeyAPI(w http.ResponseWriter, r *http.Request) {
	rotator, ok := s.store.(service.KeyRotator)
	if !ok {
		httpResponse(w, "encryption key rotation is not supported by the current store", http.StatusBadRequest)
		return
	}

	var req rotateKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Derive the new AES-256 key. If the passphrase is empty, newKey is nil
	// which tells the store to disable encryption (store plaintext).
	var newKey []byte
	if req.EncryptionKey != "" {
		var err error
		newKey, err = atcrypto.DeriveKey(req.EncryptionKey)
		if err != nil {
			httpResponse(w, fmt.Sprintf("invalid encryption key: %v", err), http.StatusBadRequest)
			return
		}
	}

	// If clustering is enabled, acquire distributed lock first.
	if s.cluster != nil {
		if err := s.cluster.Lock(r.Context()); err != nil {
			slog.Error("failed to acquire distributed lock for key rotation", "error", err)
			httpResponse(w, fmt.Sprintf("failed to acquire distributed lock: %v", err), http.StatusServiceUnavailable)
			return
		}
		defer func() {
			if err := s.cluster.Unlock(); err != nil {
				slog.Error("failed to release distributed lock", "error", err)
			}
		}()
	}

	if err := rotator.RotateEncryptionKey(r.Context(), newKey); err != nil {
		slog.Error("encryption key rotation failed", "error", err)
		httpResponse(w, fmt.Sprintf("key rotation failed: %v", err), http.StatusInternalServerError)
		return
	}

	// If clustering is enabled, broadcast the new key to all peers.
	if s.cluster != nil {
		if err := s.cluster.BroadcastNewKey(r.Context(), newKey); err != nil {
			// Rotation succeeded in DB but broadcast failed. Log prominently
			// so the operator knows peer instances may need a restart.
			slog.Error("key rotation succeeded but peer broadcast failed — other instances may need a restart",
				"error", err,
			)
		}
	}

	httpResponse(w, "encryption key rotated successfully", http.StatusOK)
}
