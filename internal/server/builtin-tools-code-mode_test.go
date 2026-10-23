package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func codeModeTestToolset(calls *atomic.Int32, inFlight, peak *atomic.Int32) *service.CodeModeToolset {
	return &service.CodeModeToolset{
		Tools: []service.Tool{{Name: "add"}, {Name: "echo"}, {Name: "fail"}, {Name: "slow"}},
		Call: func(ctx context.Context, name string, args map[string]any) (string, error) {
			calls.Add(1)
			switch name {
			case "add":
				a, _ := args["a"].(int64)
				b, _ := args["b"].(int64)
				return fmt.Sprintf(`{"sum": %d}`, a+b), nil
			case "echo":
				text, _ := args["text"].(string)
				return text, nil
			case "fail":
				return "", errors.New("upstream said no")
			case "slow":
				n := inFlight.Add(1)
				for {
					p := peak.Load()
					if n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				defer inFlight.Add(-1)
				select {
				case <-time.After(20 * time.Millisecond):
					return "ok", nil
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return "", fmt.Errorf("unknown %s", name)
		},
	}
}

func TestRunCodeMode(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		want    string
		wantErr string
		calls   int32
	}{
		{name: "chains results as data", code: `const r = await tools.add({a: 2, b: 3}); return (await tools.add({a: r.sum, b: 10})).sum;`, want: "15", calls: 2},
		{name: "text results stay strings", code: `return await tools.echo({text: "hello"});`, want: "hello", calls: 1},
		{name: "objects are returned as JSON", code: `return {x: 1, y: [1, 2]};`, want: "{\n  \"x\": 1,\n  \"y\": [\n    1,\n    2\n  ]\n}"},
		{name: "fan out with allSettled", code: `const r = await Promise.allSettled([tools.echo({text: "a"}), tools.fail({}), tools.echo({text: "b"})]); return r.map(x => x.status === "fulfilled" ? x.value : x.reason.message).join(",");`, want: "a,upstream said no,b", calls: 3},
		{name: "tool errors are catchable", code: `try { await tools.fail({}); } catch (e) { return "caught: " + e.message; }`, want: "caught: upstream said no", calls: 1},
		{name: "uncaught tool error fails the script", code: `await tools.fail({});`, wantErr: "upstream said no", calls: 1},
		{name: "unknown tool is not defined", code: `await tools["nope"]({});`, wantErr: "no member 'nope'"},
		{name: "console output is returned", code: `console.log("step", {n: 1}); return "done";`, want: "done\n\nConsole output:\nstep {\"n\":1}\n"},
		{name: "syntax error", code: `return (`, wantErr: "script failed"},
		{name: "awaiting nothing that settles", code: `await new Promise(() => {});`, wantErr: "never finishes"},
		{name: "search tools", code: `return searchTools("ech").map(t => t.name).join();`, want: "echo"},
		{name: "no return", code: `await tools.echo({text: "x"});`, want: "(the script returned nothing)", calls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls, inFlight, peak atomic.Int32
			got, err := runCodeMode(context.Background(), tt.code, codeModeTestToolset(&calls, &inFlight, &peak), nil)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q (result %q)", err, tt.wantErr, got)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if calls.Load() != tt.calls {
				t.Fatalf("calls = %d, want %d", calls.Load(), tt.calls)
			}
		})
	}
}

func TestRunCodeModeLimits(t *testing.T) {
	t.Run("concurrency is bounded", func(t *testing.T) {
		var calls, inFlight, peak atomic.Int32
		got, err := runCodeMode(context.Background(), `return (await Promise.all(Array.from({length: 40}, () => tools.slow({})))).length;`, codeModeTestToolset(&calls, &inFlight, &peak), nil)
		if err != nil || got != "40" {
			t.Fatalf("got %q, %v", got, err)
		}
		if peak.Load() > codeModeMaxConcurrency || peak.Load() < 2 {
			t.Fatalf("peak concurrency = %d", peak.Load())
		}
	})
	t.Run("call budget", func(t *testing.T) {
		var calls, inFlight, peak atomic.Int32
		_, err := runCodeMode(context.Background(), `await Promise.all(Array.from({length: 300}, () => tools.echo({text: "x"})));`, codeModeTestToolset(&calls, &inFlight, &peak), nil)
		if err == nil || !strings.Contains(err.Error(), "at most 256") {
			t.Fatalf("err = %v", err)
		}
		if calls.Load() > codeModeMaxCalls {
			t.Fatalf("calls = %d", calls.Load())
		}
	})
	t.Run("deadline interrupts a busy loop", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		var calls, inFlight, peak atomic.Int32
		_, err := runCodeMode(ctx, `while (true) {}`, codeModeTestToolset(&calls, &inFlight, &peak), nil)
		if err == nil || !strings.Contains(err.Error(), "ran out of time") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("arguments must be an object", func(t *testing.T) {
		var calls, inFlight, peak atomic.Int32
		_, err := runCodeMode(context.Background(), `await tools.echo("x");`, codeModeTestToolset(&calls, &inFlight, &peak), nil)
		if err == nil || !strings.Contains(err.Error(), "must be an object") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestCodeModeTimeoutFloor(t *testing.T) {
	if got := codeModeTimeout(60 * time.Second); got != codeModeMinTimeout {
		t.Fatalf("60s tool timeout -> %v, want %v", got, codeModeMinTimeout)
	}
	if got := codeModeTimeout(time.Hour); got != time.Hour {
		t.Fatalf("a longer tool timeout must be kept, got %v", got)
	}
}

// The fallback toolset is the built-ins in the caller's scope, never the
// orchestration tools themselves.
func TestCodeModeFallbackToolsetFollowsScope(t *testing.T) {
	s := &Server{}
	var revoked atomic.Bool
	ctx := service.ContextWithBuiltinToolScope(runtimeTestContext(t, t.TempDir(), &revoked), func(name string) bool {
		return name == "file_list" || name == "file_read" || name == "batch_execute"
	})
	toolset, _, err := s.codeModeToolset(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range toolset.Tools {
		names = append(names, tool.Name)
	}
	// batch_execute is in scope and allowed, but orchestration tools never nest.
	if strings.Join(names, ",") != "file_read,file_list" {
		t.Fatalf("tools = %v", names)
	}
}
