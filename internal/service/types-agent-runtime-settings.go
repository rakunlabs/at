package service

import (
	"context"
	"errors"
	"fmt"
)

const (
	DefaultMaxBackgroundSubagentsPerOwner = 16
	MaxBackgroundSubagentsPerOwner        = 128
)

var ErrAgentRuntimeSettingsConflict = errors.New("agent runtime settings changed; reload before saving")

// AgentRuntimeSettings controls installation-wide limits for ephemeral agent runs.
type AgentRuntimeSettings struct {
	Version                        int64 `json:"version"`
	MaxBackgroundSubagentsPerOwner int   `json:"max_background_subagents_per_owner"`
}

func DefaultAgentRuntimeSettings() AgentRuntimeSettings {
	return AgentRuntimeSettings{
		Version:                        1,
		MaxBackgroundSubagentsPerOwner: DefaultMaxBackgroundSubagentsPerOwner,
	}
}

func (s AgentRuntimeSettings) Validate() error {
	if s.Version < 1 {
		return fmt.Errorf("invalid settings version")
	}
	if s.MaxBackgroundSubagentsPerOwner < 1 || s.MaxBackgroundSubagentsPerOwner > MaxBackgroundSubagentsPerOwner {
		return fmt.Errorf("max background subagents per owner must be between 1 and %d", MaxBackgroundSubagentsPerOwner)
	}
	return nil
}

type AgentRuntimeSettingsStorer interface {
	GetAgentRuntimeSettings(ctx context.Context) (*AgentRuntimeSettings, error)
	SaveAgentRuntimeSettings(ctx context.Context, settings AgentRuntimeSettings) (*AgentRuntimeSettings, error)
}
