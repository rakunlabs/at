package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
)

var (
	name    = "at"
	version = "v0.0.0"
)

func main() {
	config.Service = name + "/" + version

	into.Init(run,
		into.WithLogger(logi.InitializeLog(logi.WithCaller(false))),
		into.WithMsgf("%s [%s]", name, version),
	)
}

// ///////////////////////////////////////////////////////////////////

func run(ctx context.Context) error {
	cfg, err := config.Load(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	slog.Info("connecting to MCP server", "url", cfg.MCPServerURL)
	mcpClient, err := service.NewHTTPMCPClient(ctx, cfg.MCPServerURL)
	if err != nil {
		return fmt.Errorf("failed to connect to MCP server: %w", err)
	}
	defer mcpClient.Close()

	// Choose your LLM provider
	var provider service.LLMProvider

	switch cfg.SelectLLM {
	case "antropic":
		if cfg.LLM.Antropic.APIKey == "" {
			return fmt.Errorf("Antropic API key is not configured")
		}

		slog.Info("using Antropic LLM provider")
		provider, err = antropic.New(cfg.LLM.Antropic.APIKey, cfg.LLM.Antropic.Model)
		if err != nil {
			return fmt.Errorf("failed to create Antropic provider: %w", err)
		}
	default:
		return fmt.Errorf("no LLM provider configured")
	}

	// Create agent
	agent := service.NewAgent(mcpClient, provider)
	if err := agent.SetTools(ctx); err != nil {
		return fmt.Errorf("failed to set tools: %w", err)
	}

	// Run conversation loop
BREAK_LOOP:
	for {
		fmt.Print("Enter your message (or 'quit' to exit): ")
		inputChan := make(chan string, 1)
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			if scanner.Scan() {
				inputChan <- scanner.Text()
			} else {
				inputChan <- ""
			}
		}()
		select {
		case message := <-inputChan:
			if message == "quit" {
				break BREAK_LOOP
			}
			if err := agent.Run(ctx, message); err != nil {
				return fmt.Errorf("agent run failed: %w", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}
