package service

import "context"

// GatewayMCPAdmissionStorer exposes only routing metadata before admission. An
// empty workspace selects public endpoints only; ambiguous public names fail.
// Credentials and executable configuration are loaded only after binding.
type GatewayMCPAdmissionStorer interface {
	GetGatewayMCPRoute(context.Context, string, string) (*MCPServer, error)
	GetExecutionMCPServer(context.Context, string) (*MCPServer, error)
}

// ExecutionBotConfigStorer supplies the platform token solely to the bot's
// authenticated service dispatcher, independent of human secret DTO redaction.
type ExecutionBotConfigStorer interface {
	GetExecutionBotConfig(context.Context, string) (*BotConfig, error)
}
