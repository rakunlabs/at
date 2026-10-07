package server

import "github.com/rakunlabs/ada"

// Register on the existing API group: its workspace admission, feature gate
// and middleware chain are inherited unchanged. Do not create a second group.
func (s *Server) registerChatSessionRoutes(api *ada.Mux) {
	api.GET("/v1/chat/sessions", s.ListChatSessionsAPI)
	api.POST("/v1/chat/sessions", s.CreateChatSessionAPI)
	api.GET("/v1/chat/sessions/{id}", s.GetChatSessionAPI)
	api.PUT("/v1/chat/sessions/{id}", s.UpdateChatSessionAPI)
	api.DELETE("/v1/chat/sessions/{id}", s.DeleteChatSessionAPI)
	api.GET("/v1/chat/sessions/{id}/messages", s.ListChatMessagesAPI)
	api.DELETE("/v1/chat/sessions/{id}/messages", s.DeleteChatMessagesAPI)
	api.POST("/v1/chat/sessions/{id}/messages", s.SendChatMessageAPI)
	api.GET("/v1/chat/sessions/{id}/streams/{stream}", s.ChatStreamAPI)
	api.DELETE("/v1/chat/sessions/{id}/streams/{stream}", s.ChatStreamAPI)
	api.POST("/v1/chat/sessions/{id}/confirm", s.ConfirmToolCallAPI)
}

func (s *Server) registerChatWorkbenchRoutes(api *ada.Mux) {
	// Shared completion endpoint used by embedded AI assistants.
	api.POST("/v1/chat/completions", s.AdminChatCompletions)

	// Per-account transcripts and replay.
	api.GET("/v1/chats/conversations", s.PlaygroundConversationsAPI)
	api.POST("/v1/chats/completions", s.AdminChatCompletions)
	api.GET("/v1/chats/streams/{stream}", s.ChatStreamAPI)
	api.DELETE("/v1/chats/streams/{stream}", s.ChatStreamAPI)
	api.POST("/v1/chats/conversations", s.PlaygroundConversationsAPI)
	api.GET("/v1/chats/conversations/{id}", s.PlaygroundConversationAPI)
	api.PATCH("/v1/chats/conversations/{id}", s.PlaygroundConversationAPI)
	api.DELETE("/v1/chats/conversations/{id}", s.PlaygroundConversationAPI)
	api.POST("/v1/chats/conversations/{id}/fork", s.PlaygroundForkAPI)
	api.GET("/v1/chats/conversations/{id}/messages", s.PlaygroundMessagesAPI)
	api.POST("/v1/chats/conversations/{id}/messages", s.PlaygroundMessagesAPI)
	api.DELETE("/v1/chats/conversations/{id}/messages", s.PlaygroundMessagesAPI)

	// Explicit chat sharing, with its own feature and ownership checks.
	api.POST("/v1/chats/conversations/{id}/shares/preview", s.ChatSharePreviewAPI)
	api.POST("/v1/chats/conversations/{id}/shares", s.PublishChatShareAPI)
	api.GET("/v1/chats/conversations/{id}/share", s.ChatConversationShareAPI)
	api.GET("/v1/chats/shares/{id}", s.ChatShareAPI)
	api.PUT("/v1/chats/shares/{id}", s.ChatShareAPI)
	api.DELETE("/v1/chats/shares/{id}", s.ChatShareAPI)
	api.POST("/v1/chats/shares/{id}/import", s.ImportChatShareAPI)
	api.GET("/v1/chats/shares/{id}/media/{media}", s.ChatShareMediaAPI)

	api.GET("/v1/chats/defaults", s.PlaygroundDefaultsAPI)
	api.PUT("/v1/chats/defaults", s.PlaygroundDefaultsAPI)
	api.GET("/v1/chats/presets", s.ChatPresetsAPI)
	api.PUT("/v1/chats/presets", s.ChatPresetsAPI)
	api.GET("/v1/chats/workspace-presets", s.WorkspaceChatPresetsAPI)
	api.POST("/v1/chats/workspace-presets", s.WorkspaceChatPresetsAPI)
	api.PUT("/v1/chats/workspace-presets/{id}", s.WorkspaceChatPresetsAPI)
	api.DELETE("/v1/chats/workspace-presets/{id}", s.WorkspaceChatPresetsAPI)
	api.GET("/v1/chats/commands", s.ChatCommandsAPI)
	api.PUT("/v1/chats/commands", s.ChatCommandsAPI)
	api.GET("/v1/chats/workspace-commands", s.WorkspaceChatCommandsAPI)
	api.POST("/v1/chats/workspace-commands", s.WorkspaceChatCommandsAPI)
	api.PUT("/v1/chats/workspace-commands/{id}", s.WorkspaceChatCommandsAPI)
	api.DELETE("/v1/chats/workspace-commands/{id}", s.WorkspaceChatCommandsAPI)

	// Stored here, but local MCP tools are dialled only by the browser.
	api.GET("/v1/chats/local-mcp-servers", s.LocalMCPServersAPI)
	api.PUT("/v1/chats/local-mcp-servers", s.LocalMCPServersAPI)
	api.POST("/v1/chats/local-mcp-servers/{id}/reveal", s.LocalMCPServerRevealAPI)
	api.POST("/v1/chats/tool-observations", s.ChatToolObservationAPI)
	// Stored here, but local providers are called only by the browser.
	api.GET("/v1/chats/local-providers", s.LocalChatProvidersAPI)
	api.PUT("/v1/chats/local-providers", s.LocalChatProvidersAPI)
	api.POST("/v1/chats/local-providers/{id}/reveal", s.LocalChatProviderRevealAPI)
	api.POST("/v1/chats/generation-observations", s.ChatGenerationObservationAPI)
	api.POST("/v1/chats/skill-runs", s.ChatSkillRunAPI)
	api.GET("/v1/chats/skill-runs/{id}", s.ChatSkillRunStatusAPI)
}
