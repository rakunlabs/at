# openai-compatible

A small, dependency-light Go client for any server that speaks the OpenAI
Chat Completions wire format. Drop it in front of:

- the official OpenAI API (`https://api.openai.com/v1`)
- the **AT gateway** (`http://<host>/gateway/v1`)
- Ollama, vLLM, LiteLLM, Together, Groq, GitHub Models, Azure OpenAI, …

Anything that exposes `POST /chat/completions`, `POST /embeddings`, and
`GET /models` in OpenAI shape will work.

## Install

```sh
go get github.com/rakunlabs/at/pkg/openai-compatible
```

## Quick start

```go
import oa "github.com/rakunlabs/at/pkg/openai-compatible"

client, err := oa.New(
    oa.WithBaseURL("http://localhost:8080/gateway/v1"), // or DefaultBaseURL
    oa.WithAPIKey(os.Getenv("AT_API_TOKEN")),
    oa.WithModel("openai/gpt-4o-mini"),
)
if err != nil { log.Fatal(err) }

resp, err := client.Chat(ctx, &oa.ChatRequest{
    Messages: []oa.Message{
        oa.SystemMessage("You are a helpful assistant."),
        oa.UserMessage("Hello!"),
    },
})
if err != nil { log.Fatal(err) }

fmt.Println(resp.Content())
```

## Streaming

```go
stream, err := client.ChatStream(ctx, &oa.ChatRequest{
    Messages: []oa.Message{oa.UserMessage("Tell me a haiku about Go.")},
})
if err != nil { log.Fatal(err) }
defer stream.Close()

for {
    ev, err := stream.Recv()
    if errors.Is(err, io.EOF) { break }
    if err != nil { log.Fatal(err) }

    for _, ch := range ev.Choices {
        fmt.Print(ch.Delta.Content)
    }
}
```

To get the assembled final response (with reassembled tool-call arguments):

```go
final, err := oa.AccumulateStream(stream, func(ev *oa.StreamEvent) {
    // optional: forward each chunk to a UI
})
```

## Tool / function calling

```go
weather := oa.FunctionTool(
    "get_weather",
    "Return the current weather for a city.",
    map[string]any{
        "type": "object",
        "properties": map[string]any{
            "city": map[string]any{"type": "string"},
        },
        "required": []string{"city"},
    },
)

resp, _ := client.Chat(ctx, &oa.ChatRequest{
    Messages: []oa.Message{oa.UserMessage("Weather in Istanbul?")},
    Tools:    []oa.Tool{weather},
})

for _, tc := range resp.ToolCalls() {
    args, _ := tc.ArgumentsMap()
    result := callMyTool(tc.Function.Name, args)
    // Feed the result back as a tool message and call Chat again
    // (see example/main.go for the full loop).
}
```

## Multimodal content

```go
msg := oa.UserMessageParts(
    oa.TextPart("What's in this picture?"),
    oa.ImageURLPart("https://example.com/cat.jpg", "auto"),
)
// or attach inline base64:
oa.ImageDataPart("image/png", pngBytes, "low")
oa.InputAudioPart(wavBytes, "wav")
oa.FilePartByID("file-abc123")
```

## Embeddings

```go
emb, _ := client.Embeddings(ctx, &oa.EmbeddingRequest{
    Model: "text-embedding-3-small",
    Input: []string{"hello", "world"},
})
v, _ := emb.Data[0].AsFloat()
```

## List models

```go
list, _ := client.ListModels(ctx)
for _, m := range list.Data {
    fmt.Println(m.ID)
}
```

## Errors

Non-2xx responses are returned as a typed `*APIError`. Rate-limited
responses (HTTP 429 or `error.type == "rate_limit_error"`) are returned as
`*RateLimitError` with the parsed `Retry-After` header:

```go
if rle := (*oa.RateLimitError)(nil); errors.As(err, &rle) {
    time.Sleep(rle.RetryAfter)
}
```

## Options

| Option                       | Purpose                                     |
| ---                          | ---                                         |
| `WithBaseURL`                | API root (with or without `/chat/completions`) |
| `WithAPIKey`                 | Bearer token                                |
| `WithModel`                  | Default model id                            |
| `WithHeader` / `WithHeaders` | Extra request headers                       |
| `WithUserAgent`              | Override `User-Agent`                       |
| `WithProxy`                  | HTTP/HTTPS/SOCKS5 proxy                     |
| `WithInsecureSkipVerify`     | Skip TLS verification (dev only)            |
| `WithTimeout`                | Overall request timeout                     |
| `WithDisableRetry`           | Turn off retry-with-backoff                 |
| `WithRetryMax`               | Max retry attempts (default 4)              |
| `WithHTTPClient`             | Use a caller-supplied `*http.Client`        |
| `WithOKOptions`              | Forward arbitrary `ok.OptionClientFn` options |

`ChatRequest.Extra` is a `map[string]any` that gets merged into the
on-the-wire JSON for any provider-specific fields the typed struct does not
yet expose (e.g. `web_search_options`, `thinking`, `top_k`, `min_p`, …).

## Run the example

```sh
cd pkg/openai-compatible
go run ./example \
    -base-url "http://localhost:8080/gateway/v1" \
    -api-key  "$AT_API_TOKEN" \
    -model    "openai/gpt-4o-mini"
```
