package service

import "context"

// Credential use is deliberately separate from management/inspection DTOs.
// These methods are for an authorized runtime adapter, never an HTTP response.
type WorkspaceCredentialStorer interface {
	ResolveVariableForUse(context.Context, string) (*Variable, error)
	ResolveConnectionForUse(context.Context, string) (*Connection, error)
	ResolveNodeConfigForUse(context.Context, string) (*NodeConfig, error)
	ResolveMCPSetForUse(context.Context, string) (*MCPSet, error)
	ResolveMCPServerForUse(context.Context, string) (*MCPServer, error)
	ResolveBotForUse(context.Context, string) (*BotConfig, error)
}
