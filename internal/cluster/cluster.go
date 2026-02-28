// Package cluster provides distributed coordination for multiple AT instances
// using the alan UDP peer discovery library. It wraps alan to provide:
//   - Distributed locking for admin operations (e.g., key rotation)
//   - Broadcasting encryption key updates to all peers
package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/rakunlabs/alan"
)

const (
	// lockKeyRotation is the distributed lock name for key rotation.
	lockKeyRotation = "encryption-key-rotation"

	// lockScheduler is the distributed lock name for cron scheduler.
	lockScheduler = "cron-scheduler"

	// msgTypeRotateKey identifies a key rotation broadcast message.
	msgTypeRotateKey = "rotate-key"
)

// clusterMessage is the JSON envelope for messages sent between peers.
type clusterMessage struct {
	Type string `json:"type"`
	// Key is base64-encoded new encryption key (empty = disable encryption).
	Key string `json:"key,omitempty"`
}

// Cluster wraps an alan instance with AT-specific distributed coordination.
type Cluster struct {
	alan *alan.Alan
}

// New creates a Cluster from the server's alan configuration.
// Returns nil, nil if cfg is nil (clustering disabled).
func New(cfg *alan.Config) (*Cluster, error) {
	if cfg == nil {
		return nil, nil
	}

	a, err := alan.New(*cfg)
	if err != nil {
		return nil, fmt.Errorf("create alan instance: %w", err)
	}

	return &Cluster{alan: a}, nil
}

// Start begins the alan peer discovery system in the background.
// The onNewKey callback is invoked when this instance receives a key rotation
// broadcast from another peer. The callback receives the new derived AES key
// (nil means encryption was disabled).
//
// Start blocks until the context is cancelled. It should be run in a goroutine.
func (c *Cluster) Start(ctx context.Context, onNewKey func(newKey []byte)) error {
	c.alan.OnPeerJoin(func(addr *net.UDPAddr) {
		slog.Info("cluster peer joined", "addr", addr.String())
	})

	c.alan.OnPeerLeave(func(addr *net.UDPAddr) {
		slog.Info("cluster peer left", "addr", addr.String())
	})

	handler := func(_ context.Context, msg alan.Message) {
		var cm clusterMessage
		if err := json.Unmarshal(msg.Data, &cm); err != nil {
			slog.Warn("cluster: invalid message", "from", msg.Addr, "error", err)
			return
		}

		switch cm.Type {
		case msgTypeRotateKey:
			var newKey []byte
			if cm.Key != "" {
				var err error
				newKey, err = base64.StdEncoding.DecodeString(cm.Key)
				if err != nil {
					slog.Error("cluster: invalid key in rotate-key message", "from", msg.Addr, "error", err)
					return
				}
			}

			slog.Info("cluster: received key rotation from peer", "from", msg.Addr)

			if onNewKey != nil {
				onNewKey(newKey)
			}

			// Reply with ack if this is a request.
			if msg.IsRequest() {
				c.alan.Reply(msg, []byte("ok")) //nolint:errcheck
			}

		default:
			slog.Debug("cluster: unknown message type", "type", cm.Type, "from", msg.Addr)
		}
	}

	return c.alan.Start(ctx, handler)
}

// Stop gracefully leaves the cluster.
func (c *Cluster) Stop() error {
	return c.alan.Stop()
}

// Lock acquires the distributed lock for key rotation.
// Blocks until the lock is acquired or the context is cancelled.
func (c *Cluster) Lock(ctx context.Context) error {
	return c.alan.Lock(ctx, lockKeyRotation)
}

// Unlock releases the distributed lock for key rotation.
func (c *Cluster) Unlock() error {
	return c.alan.Unlock(lockKeyRotation)
}

// LockScheduler acquires the distributed lock for the cron scheduler.
// Blocks until the lock is acquired or the context is cancelled.
func (c *Cluster) LockScheduler(ctx context.Context) error {
	return c.alan.Lock(ctx, lockScheduler)
}

// UnlockScheduler releases the distributed lock for the cron scheduler.
func (c *Cluster) UnlockScheduler() error {
	return c.alan.Unlock(lockScheduler)
}

// BroadcastNewKey sends the new encryption key to all peers and waits for
// their acknowledgements. The key bytes are base64-encoded and sent over
// alan's (optionally ChaCha20-encrypted) UDP channel.
// A nil newKey signals peers to disable encryption.
func (c *Cluster) BroadcastNewKey(ctx context.Context, newKey []byte) error {
	peers := c.alan.Peers()
	if len(peers) == 0 {
		slog.Info("cluster: no peers to broadcast key rotation to")
		return nil
	}

	cm := clusterMessage{
		Type: msgTypeRotateKey,
	}
	if newKey != nil {
		cm.Key = base64.StdEncoding.EncodeToString(newKey)
	}

	data, err := json.Marshal(cm)
	if err != nil {
		return fmt.Errorf("marshal cluster message: %w", err)
	}

	// Use a timeout so we don't wait forever for unresponsive peers.
	broadcastCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	replies, err := c.alan.SendAndWaitReply(broadcastCtx, data)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("broadcast key rotation: %w", err)
	}

	slog.Info("cluster: key rotation broadcast complete",
		"peers", len(peers),
		"acks", len(replies),
	)

	if len(replies) < len(peers) {
		slog.Warn("cluster: not all peers acknowledged key rotation",
			"expected", len(peers),
			"received", len(replies),
		)
	}

	return nil
}

// Ready returns a channel that is closed when the cluster is ready.
func (c *Cluster) Ready() <-chan struct{} {
	return c.alan.Ready()
}
