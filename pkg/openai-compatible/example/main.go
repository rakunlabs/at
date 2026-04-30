// Package main is a runnable example that exercises every public capability
// of github.com/rakunlabs/at/pkg/openai-compatible against any
// OpenAI-compatible server (OpenAI, the AT gateway, Ollama, vLLM, …).
//
// Run it with:
//
//	go run ./example \
//	    -base-url "http://localhost:8080/gateway/v1" \
//	    -api-key  "$AT_API_TOKEN" \
//	    -model    "openai/gpt-4o-mini"
//
// Or against the real OpenAI API:
//
//	go run ./example \
//	    -base-url "https://api.openai.com/v1" \
//	    -api-key  "$OPENAI_API_KEY" \
//	    -model    "gpt-4o-mini"
//
// The example walks through:
//
//  1. A plain chat completion.
//  2. A streaming chat completion.
//  3. A chat completion that uses a tool (function calling).
//  4. Listing models.
//  5. Computing an embedding (skipped if -embed-model is empty).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	oa "github.com/rakunlabs/at/pkg/openai-compatible"
)

func main() {
	var (
		baseURL    = flag.String("base-url", oa.DefaultBaseURL, "OpenAI-compatible API root")
		apiKey     = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "Bearer token (env OPENAI_API_KEY by default)")
		model      = flag.String("model", "gpt-4o-mini", "Model id, e.g. openai/gpt-4o-mini")
		embedModel = flag.String("embed-model", "", "Embedding model id (skip embedding step if empty)")
		insecure   = flag.Bool("insecure", false, "Skip TLS verification")
		proxy      = flag.String("proxy", "", "HTTP/HTTPS/SOCKS5 proxy URL")
		timeout    = flag.Duration("timeout", 60*time.Second, "Per-request timeout")
		skipStream = flag.Bool("skip-stream", false, "Skip the streaming demo")
		skipTool   = flag.Bool("skip-tool", false, "Skip the tool-calling demo")
		skipModels = flag.Bool("skip-models", false, "Skip the list-models demo")
	)
	flag.Parse()

	client, err := oa.New(
		oa.WithBaseURL(*baseURL),
		oa.WithAPIKey(*apiKey),
		oa.WithModel(*model),
		oa.WithInsecureSkipVerify(*insecure),
		oa.WithProxy(*proxy),
		oa.WithTimeout(*timeout),
		oa.WithUserAgent("at-openai-compatible-example/0.1"),
	)
	if err != nil {
		log.Fatalf("client: %v", err)
	}

	ctx := context.Background()

	step("1. plain chat completion", func() error { return demoChat(ctx, client) })
	if !*skipStream {
		step("2. streaming chat completion", func() error { return demoStream(ctx, client) })
	}
	if !*skipTool {
		step("3. tool / function calling", func() error { return demoTool(ctx, client) })
	}
	if !*skipModels {
		step("4. list models", func() error { return demoModels(ctx, client) })
	}
	if *embedModel != "" {
		step("5. embeddings", func() error { return demoEmbeddings(ctx, client, *embedModel) })
	}
}

func step(title string, fn func() error) {
	fmt.Println()
	fmt.Println("── " + title + " ──")
	if err := fn(); err != nil {
		var rle *oa.RateLimitError
		if errors.As(err, &rle) {
			log.Printf("rate limited; server suggests retry-after %s", rle.RetryAfter)
		}
		log.Printf("ERR: %v", err)
	}
}

// 1. Plain chat completion.
func demoChat(ctx context.Context, c *oa.Client) error {
	resp, err := c.Chat(ctx, &oa.ChatRequest{
		Messages: []oa.Message{
			oa.SystemMessage("You are a terse Go programmer."),
			oa.UserMessage("In one sentence: what is a Go interface?"),
		},
		Temperature: new(0.2),
		MaxTokens:   new(120),
	})
	if err != nil {
		return err
	}
	fmt.Println(resp.Content())
	if resp.Usage != nil {
		fmt.Printf("[usage] prompt=%d completion=%d total=%d\n",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	}
	return nil
}

// 2. Streaming chat completion — print each delta as it arrives, then
//    show the assembled final response.
func demoStream(ctx context.Context, c *oa.Client) error {
	stream, err := c.ChatStream(ctx, &oa.ChatRequest{
		Messages: []oa.Message{
			oa.UserMessage("Count from one to five, separated by spaces."),
		},
		MaxTokens: new(60),
	})
	if err != nil {
		return err
	}
	defer stream.Close()

	fmt.Print("stream: ")
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			fmt.Println()
			break
		}
		if err != nil {
			return err
		}
		for _, ch := range ev.Choices {
			fmt.Print(ch.Delta.Content)
		}
	}
	return nil
}

// 3. Tool / function calling — define a tool, let the model decide to call
//    it, run the local handler, feed the result back, and print the final
//    answer.
func demoTool(ctx context.Context, c *oa.Client) error {
	weatherTool := oa.FunctionTool(
		"get_weather",
		"Return the current weather for a city in Celsius.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string", "description": "City name"},
			},
			"required":             []string{"city"},
			"additionalProperties": false,
		},
	)

	messages := []oa.Message{
		oa.SystemMessage("Use the get_weather tool whenever the user asks about weather."),
		oa.UserMessage("What's the weather in Istanbul right now?"),
	}

	first, err := c.Chat(ctx, &oa.ChatRequest{
		Messages: messages,
		Tools:    []oa.Tool{weatherTool},
	})
	if err != nil {
		return err
	}

	calls := first.ToolCalls()
	if len(calls) == 0 {
		fmt.Println("(model answered without calling the tool)")
		fmt.Println(first.Content())
		return nil
	}

	// Append the assistant's tool-call request to the history.
	messages = append(messages, first.FirstChoice().Message)

	// "Execute" each tool locally and append the result.
	for _, tc := range calls {
		args, err := tc.ArgumentsMap()
		if err != nil {
			return fmt.Errorf("decode tool args: %w", err)
		}
		fmt.Printf("model called %s(%v)\n", tc.Function.Name, args)
		result := runWeather(args)
		messages = append(messages, oa.ToolMessage(tc.ID, result))
	}

	// Second turn: feed the tool result back, no tools this round.
	final, err := c.Chat(ctx, &oa.ChatRequest{
		Messages: messages,
	})
	if err != nil {
		return err
	}
	fmt.Println(final.Content())
	return nil
}

// runWeather is the trivial "tool implementation" used by the demo.
func runWeather(args map[string]any) string {
	city, _ := args["city"].(string)
	return fmt.Sprintf(`{"city": %q, "temp_c": 18, "condition": "partly cloudy"}`, city)
}

// 4. List models.
func demoModels(ctx context.Context, c *oa.Client) error {
	list, err := c.ListModels(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("got %d models, first 10:\n", len(list.Data))
	for i, m := range list.Data {
		if i >= 10 {
			break
		}
		fmt.Printf("  - %s (owned_by=%s)\n", m.ID, m.OwnedBy)
	}
	return nil
}

// 5. Embeddings.
func demoEmbeddings(ctx context.Context, c *oa.Client, embedModel string) error {
	resp, err := c.Embeddings(ctx, &oa.EmbeddingRequest{
		Model: embedModel,
		Input: []string{
			"Hello, world.",
			"OpenAI-compatible servers all speak the same wire format.",
		},
	})
	if err != nil {
		return err
	}
	for _, d := range resp.Data {
		v, err := d.AsFloat()
		if err != nil {
			return err
		}
		fmt.Printf("  vec[%d] dims=%d preview=%v...\n", d.Index, len(v), v[:min3(3, len(v))])
	}
	if resp.Usage != nil {
		// embeddings usage may only have prompt_tokens populated
		raw, _ := json.Marshal(resp.Usage)
		fmt.Printf("[usage] %s\n", raw)
	}
	return nil
}

func min3(a, b int) int {
	if a < b {
		return a
	}
	return b
}
