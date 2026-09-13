package common

import (
	"context"
	"io"

	"github.com/rakunlabs/at/internal/service"
)

// StreamWithContext relays a provider parser's bounded channel. When a caller
// stops consuming and cancels, close the HTTP body and drain the parser so a
// blocked send cannot strand its goroutine or per-provider concurrency slot.
// The parser owns closing source and releasing its resources.
func StreamWithContext(ctx context.Context, source <-chan service.StreamChunk, body io.Closer) <-chan service.StreamChunk {
	out := make(chan service.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			_ = body.Close()
			for range source {
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-source:
				if !ok {
					return
				}
				select {
				case out <- chunk:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}
