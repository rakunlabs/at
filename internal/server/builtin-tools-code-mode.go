package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// code_execute runs a short JavaScript program that calls the run's own tools
// (built-ins and MCP tools) and returns one composed result, so several tool
// results never pass through the model's context one by one. The VM has no
// host access of its own: no HTTP, files or variables, only `tools`, `console`
// and `searchTools`. Every nested call goes through the same admission as a
// direct call on the surface the script runs on.
const (
	codeModeToolName       = "code_execute"
	codeModeMaxCalls       = 256
	codeModeMaxConcurrency = 16
	codeModeMaxCodeBytes   = 256 << 10
	codeModeMaxLogBytes    = 256 << 10
	// codeModeMinTimeout is the least a script gets: it runs many tool calls,
	// so the per-tool deadline (60s by default) would cut it short. Each
	// nested call still has the per-tool deadline.
	codeModeMinTimeout = 15 * time.Minute
)

// codeModeTimeout is the deadline for one code_execute call on a surface whose
// per-tool deadline is toolTimeout.
func codeModeTimeout(toolTimeout time.Duration) time.Duration {
	return max(toolTimeout, codeModeMinTimeout)
}

// Tools that orchestrate other tools, need a person, or end the run are not
// reachable from a script.
var codeModeExcludedTools = map[string]bool{
	codeModeToolName: true,
	"batch_execute":  true,
	"question":       true,
	"ask_user":       true,
	"task_complete":  true,
	"task_block":     true,
}

var codeModeToolDef = builtinToolDef{
	Name: codeModeToolName,
	Description: "Run JavaScript that calls your other tools and returns one combined result. Use it to chain or fan out several tool calls (including MCP tools) " +
		"and return only what matters, instead of making the calls one by one. Inside the script: `await tools[\"tool_name\"]({...args})` calls a tool " +
		"you already have (same names and arguments); it resolves to the parsed JSON result, or the text when the result is not JSON, and rejects with an Error " +
		"when the tool fails. Run independent calls concurrently with `Promise.all` / `Promise.allSettled`. `searchTools(\"query\")` lists matching tools with their " +
		"input schemas; `console.log` output is returned too. End with an explicit `return` of the value to report. The script has no network, file or " +
		"variable access of its own. Limits: 256 tool calls per script, 16 at a time; each call keeps its normal timeout.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code": map[string]any{"type": "string", "description": "Body of an async JavaScript function. Use `await` and `return`."},
		},
		"required": []string{"code"},
	},
}

func (s *Server) execCodeExecute(ctx context.Context, args map[string]any) (string, error) {
	code, _ := args["code"].(string)
	if strings.TrimSpace(code) == "" {
		return "", errors.New("code is required")
	}
	if len(code) > codeModeMaxCodeBytes {
		return "", fmt.Errorf("code is larger than %d KiB", codeModeMaxCodeBytes>>10)
	}
	toolset, cleanup, err := s.codeModeToolset(ctx)
	if err != nil {
		return "", err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, codeModeMinTimeout)
		defer cancel()
	}
	return runCodeMode(ctx, code, toolset, s.codeModeObserver(ctx))
}

// codeModeToolset resolves the surface's recorded toolset, or the built-ins in
// scope when the surface records none.
func (s *Server) codeModeToolset(ctx context.Context) (*service.CodeModeToolset, func(), error) {
	if provider, ok := service.CodeModeToolsFromContext(ctx); ok {
		toolset, cleanup, err := provider(ctx)
		if err != nil {
			return nil, cleanup, fmt.Errorf("resolve tools: %w", err)
		}
		if toolset == nil {
			toolset = &service.CodeModeToolset{}
		}
		return filterCodeModeToolset(toolset), cleanup, nil
	}
	var tools []service.Tool
	for _, def := range builtinTools {
		if !service.BuiltinToolInScope(ctx, def.Name) || codeModeExcludedTools[def.Name] {
			continue
		}
		if service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: def.Name}) != nil || s.checkBuiltinToolFeatures(ctx, def.Name) != nil {
			continue
		}
		tools = append(tools, service.Tool{Name: def.Name, Description: def.Description, InputSchema: def.InputSchema})
	}
	return &service.CodeModeToolset{Tools: tools, Call: s.dispatchBuiltinTool}, nil, nil
}

func filterCodeModeToolset(toolset *service.CodeModeToolset) *service.CodeModeToolset {
	out := &service.CodeModeToolset{Call: toolset.Call}
	seen := map[string]bool{}
	for _, tool := range toolset.Tools {
		if tool.Name == "" || seen[tool.Name] || codeModeExcludedTools[tool.Name] {
			continue
		}
		seen[tool.Name] = true
		out.Tools = append(out.Tools, tool)
	}
	return out
}

// codeModeProvider is the toolset of an agent loop: its built-ins (minus those
// needing confirmation) and its MCP tools, dispatched like direct calls.
func (s *Server) codeModeProvider(mcp *agentMCPTools, builtins []service.Tool, excluded func(string) bool, callTimeout time.Duration) service.CodeModeProvider {
	return func(context.Context) (*service.CodeModeToolset, func(), error) {
		builtinNames := map[string]bool{}
		var tools []service.Tool
		for _, tool := range builtins {
			if excluded != nil && excluded(tool.Name) {
				continue
			}
			builtinNames[tool.Name] = true
			tools = append(tools, tool)
		}
		if mcp != nil {
			for _, tool := range mcp.Tools() {
				if builtinNames[tool.Name] || (excluded != nil && excluded(tool.Name)) {
					continue
				}
				tools = append(tools, tool)
			}
		}
		call := func(ctx context.Context, name string, args map[string]any) (string, error) {
			// The script has a longer deadline than one tool call; each nested
			// call keeps the surface's per-tool deadline.
			if callTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, callTimeout)
				defer cancel()
			}
			if builtinNames[name] {
				return s.dispatchBuiltinTool(service.ContextWithBuiltinToolScope(ctx, func(n string) bool { return builtinNames[n] }), name, args)
			}
			if mcp == nil {
				return "", fmt.Errorf("unknown tool %q", name)
			}
			if setName, ok := mcp.SetName(name); ok {
				if err := workflow.AuthorizeMCPSetTool(ctx, setName, name); err != nil {
					return "", err
				}
				return mcp.Call(ctx, name, args, 0)
			}
			if mcp.Owns(name) {
				return mcp.Call(ctx, name, args, 0)
			}
			return "", fmt.Errorf("unknown tool %q", name)
		}
		return &service.CodeModeToolset{Tools: tools, Call: call}, nil, nil
	}
}

// codeModeObserver records each nested call as a tool observation beneath the
// code_execute call, when the surface traces its tool calls.
func (s *Server) codeModeObserver(ctx context.Context) func(name string, args map[string]any, result string, err error, started time.Time) {
	if s.llmCallStore == nil {
		return nil
	}
	if _, ok := service.TraceParentFromContext(ctx); !ok {
		return nil
	}
	return func(name string, args map[string]any, result string, callErr error, started time.Time) {
		input, _ := json.Marshal(args)
		params := llmAuditParams{
			source:    "agent",
			obsType:   service.ObservationTool,
			name:      name,
			input:     string(input),
			output:    result,
			latencyMs: time.Since(started).Milliseconds(),
			startedAt: started,
			metadata:  map[string]any{"origin": codeModeToolName},
		}
		if callErr != nil {
			params.status = "error"
			params.level = service.ObservationLevelError
			params.errMsg = callErr.Error()
		}
		s.recordLLMCallAsync(ctx, params)
	}
}

type codeModeCompletion struct {
	resolve, reject func(any) error
	result          string
	err             error
}

// runCodeMode executes code in a fresh VM. The VM is single-threaded: tool
// calls run on goroutines and are settled back on this goroutine, which is
// also where every promise job runs.
func runCodeMode(ctx context.Context, code string, toolset *service.CodeModeToolset, observe func(string, map[string]any, string, error, time.Time)) (string, error) {
	callCtx, cancelCalls := context.WithCancel(ctx)
	defer cancelCalls()

	known := make(map[string]service.Tool, len(toolset.Tools))
	names := make([]string, 0, len(toolset.Tools))
	for _, tool := range toolset.Tools {
		known[tool.Name] = tool
		names = append(names, tool.Name)
	}
	sort.Strings(names)

	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	stopInterrupt := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stopInterrupt()

	done := make(chan codeModeCompletion, codeModeMaxCalls)
	slots := make(chan struct{}, codeModeMaxConcurrency)
	pending, calls := 0, 0
	var ready []codeModeCompletion

	callTool := func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		promise, resolve, reject := vm.NewPromise()
		// Refusals settle from the event loop too: settling a promise while
		// the VM is still executing this call would re-enter it.
		fail := func(err error) {
			ready = append(ready, codeModeCompletion{resolve: resolve, reject: reject, err: err})
		}
		args := map[string]any{}
		if raw := call.Argument(1); !goja.IsUndefined(raw) && !goja.IsNull(raw) {
			exported, ok := raw.Export().(map[string]any)
			if !ok {
				fail(fmt.Errorf("arguments for %q must be an object", name))
				return vm.ToValue(promise)
			}
			args = exported
		}
		if _, ok := known[name]; !ok {
			fail(fmt.Errorf("tool %q is not available here; available: %s", name, strings.Join(names, ", ")))
			return vm.ToValue(promise)
		}
		calls++
		if calls > codeModeMaxCalls {
			fail(fmt.Errorf("a script may make at most %d tool calls", codeModeMaxCalls))
			return vm.ToValue(promise)
		}
		pending++
		go func() {
			select {
			case slots <- struct{}{}:
			case <-callCtx.Done():
				done <- codeModeCompletion{resolve: resolve, reject: reject, err: callCtx.Err()}
				return
			}
			started := time.Now()
			result, err := toolset.Call(callCtx, name, args)
			<-slots
			if observe != nil {
				observe(name, args, result, err, started)
			}
			done <- codeModeCompletion{resolve: resolve, reject: reject, result: result, err: err}
		}()
		return vm.ToValue(promise)
	}

	var logs strings.Builder
	logTruncated := false
	logFn := func(prefix string) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, arg := range call.Arguments {
				parts = append(parts, codeModeLogValue(arg))
			}
			line := prefix + strings.Join(parts, " ") + "\n"
			if logs.Len()+len(line) > codeModeMaxLogBytes {
				logTruncated = true
				return goja.Undefined()
			}
			logs.WriteString(line)
			return goja.Undefined()
		}
	}
	console := vm.NewObject()
	_ = console.Set("log", logFn(""))
	_ = console.Set("info", logFn(""))
	_ = console.Set("warn", logFn("warn: "))
	_ = console.Set("error", logFn("error: "))

	toolsObj := vm.NewObject()
	for _, name := range names {
		name := name
		_ = toolsObj.Set(name, func(call goja.FunctionCall) goja.Value {
			return callTool(goja.FunctionCall{This: call.This, Arguments: append([]goja.Value{vm.ToValue(name)}, call.Arguments...)})
		})
	}
	searchTools := func(call goja.FunctionCall) goja.Value {
		query := strings.ToLower(strings.TrimSpace(call.Argument(0).String()))
		if goja.IsUndefined(call.Argument(0)) {
			query = ""
		}
		matches := []map[string]any{}
		for _, name := range names {
			tool := known[name]
			if query != "" && !strings.Contains(strings.ToLower(tool.Name+" "+tool.Description), query) {
				continue
			}
			matches = append(matches, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": tool.InputSchema})
		}
		return vm.ToValue(matches)
	}
	for key, value := range map[string]any{"console": console, "tools": toolsObj, "searchTools": searchTools} {
		if err := vm.Set(key, value); err != nil {
			return "", fmt.Errorf("code_execute: setup: %w", err)
		}
	}

	value, err := vm.RunString("(async function() {\n" + code + "\n})()")
	if err != nil {
		return "", codeModeError(ctx, err, logs.String())
	}
	promise, ok := value.Export().(*goja.Promise)
	if !ok {
		return "", errors.New("code_execute: script did not produce a promise")
	}

	settle := func(c codeModeCompletion) error {
		if c.err != nil {
			return c.reject(newJSError(vm, c.err))
		}
		return c.resolve(codeModeResultValue(c.result))
	}
	for promise.State() == goja.PromiseStatePending {
		if len(ready) > 0 {
			c := ready[0]
			ready = ready[1:]
			if err := settle(c); err != nil {
				return "", codeModeError(ctx, err, logs.String())
			}
			continue
		}
		if pending == 0 {
			return "", codeModeError(ctx, errors.New("the script is waiting for something that never finishes (only tool calls can be awaited)"), logs.String())
		}
		select {
		case c := <-done:
			pending--
			if err := settle(c); err != nil {
				return "", codeModeError(ctx, err, logs.String())
			}
		case <-ctx.Done():
			return "", codeModeError(ctx, ctx.Err(), logs.String())
		}
	}

	logText := logs.String()
	if logTruncated {
		logText += "[console output truncated]\n"
	}
	if promise.State() == goja.PromiseStateRejected {
		return "", codeModeError(ctx, fmt.Errorf("%s", codeModeLogValue(promise.Result())), logText)
	}

	result := codeModeOutput(promise.Result())
	if logText == "" {
		if result == "" {
			return "(the script returned nothing)", nil
		}
		return result, nil
	}
	if result == "" {
		return "Console output:\n" + logText, nil
	}
	return result + "\n\nConsole output:\n" + logText, nil
}

// codeModeResultValue hands a tool's JSON result to the script as data, and
// any other text as a string.
func codeModeResultValue(result string) any {
	trimmed := strings.TrimSpace(result)
	if trimmed != "" && (trimmed[0] == '{' || trimmed[0] == '[') {
		var parsed any
		if json.Unmarshal([]byte(trimmed), &parsed) == nil {
			return parsed
		}
	}
	return result
}

func codeModeOutput(value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	exported := value.Export()
	if text, ok := exported.(string); ok {
		return text
	}
	data, err := json.MarshalIndent(exported, "", "  ")
	if err != nil {
		return value.String()
	}
	return string(data)
}

func codeModeLogValue(value goja.Value) string {
	if value == nil || goja.IsUndefined(value) {
		return "undefined"
	}
	if obj, ok := value.(*goja.Object); ok {
		if _, isErr := obj.Export().(error); isErr || obj.ClassName() == "Error" {
			return value.String()
		}
		if _, isFn := goja.AssertFunction(value); !isFn {
			if data, err := json.Marshal(obj.Export()); err == nil {
				return string(data)
			}
		}
	}
	return value.String()
}

func codeModeError(ctx context.Context, err error, logs string) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) && ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		err = errors.New("the script ran out of time")
	}
	if logs != "" {
		return fmt.Errorf("script failed: %w\n\nConsole output:\n%s", err, logs)
	}
	return fmt.Errorf("script failed: %w", err)
}

// codeModeExcludedByName combines the fixed exclusions with a surface's own.
func codeModeExcludedByName(extra ...[]string) func(string) bool {
	return func(name string) bool {
		if codeModeExcludedTools[name] {
			return true
		}
		for _, list := range extra {
			if slices.Contains(list, name) {
				return true
			}
		}
		return false
	}
}

// newJSError builds a plain Error so scripts see `err.message` as the tool's
// own message.
func newJSError(vm *goja.Runtime, err error) goja.Value {
	ctor, ok := goja.AssertConstructor(vm.Get("Error"))
	if !ok {
		return vm.ToValue(err.Error())
	}
	obj, cerr := ctor(nil, vm.ToValue(err.Error()))
	if cerr != nil {
		return vm.ToValue(err.Error())
	}
	return obj
}

// codeModeBuiltins picks the built-in definitions out of a loop's tool list.
func codeModeBuiltins(tools []service.Tool, isBuiltin func(string) bool) []service.Tool {
	var out []service.Tool
	for _, tool := range tools {
		if isBuiltin(tool.Name) {
			out = append(out, tool)
		}
	}
	return out
}

type codeModeSelection struct {
	BuiltinTools []string `json:"builtin_tools,omitempty"`
	MCPSets      []string `json:"mcp_sets,omitempty"`
}

// codeModeSelectionProvider serves a browser-selected setup. It runs under the
// bound context of the code_execute call, so set admission applies as in the
// agent loops.
func (s *Server) codeModeSelectionProvider(selection codeModeSelection) service.CodeModeProvider {
	return func(ctx context.Context) (*service.CodeModeToolset, func(), error) {
		var builtins []service.Tool
		for _, name := range selection.BuiltinTools {
			if service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: name}) != nil || s.checkBuiltinToolFeatures(ctx, name) != nil {
				continue
			}
			if def, ok := builtinToolByName(name); ok {
				builtins = append(builtins, service.Tool{Name: def.Name, Description: def.Description, InputSchema: def.InputSchema})
			}
		}
		mcp := s.newAgentMCPTools("code_execute", nil)
		if len(selection.MCPSets) > 0 {
			mcp.Connect(ctx, selection.MCPSets, nil)
		}
		toolset, _, err := s.codeModeProvider(mcp, builtins, codeModeExcludedByName(), 0)(ctx)
		return toolset, mcp.Close, err
	}
}

// mcpCodeModeCatalogLimit bounds the tool list written into the system prompt;
// searchTools still finds every tool.
const mcpCodeModeCatalogLimit = 200

// mcpCodeModeTool returns the code_execute definition when an MCP code-mode
// surface may use it: the tool must pass execution and feature admission.
func (s *Server) mcpCodeModeTool(ctx context.Context) (service.Tool, bool) {
	if service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: codeModeToolName}) != nil || s.checkBuiltinToolFeatures(ctx, codeModeToolName) != nil {
		return service.Tool{}, false
	}
	return service.Tool{Name: codeModeToolDef.Name, Description: codeModeToolDef.Description, InputSchema: codeModeToolDef.InputSchema}, true
}

// mcpCodeModePrompt tells the model which MCP tools sit behind code_execute.
// Only names and the first sentence of each description are listed, which is
// what keeps a large catalog cheap; schemas come from searchTools.
func mcpCodeModePrompt(tools []service.Tool) string {
	if len(tools) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## MCP tools (through code_execute)\n")
	b.WriteString("These tools are not in your tool list. Call them from `code_execute`: `await tools[\"name\"]({...})`. ")
	b.WriteString("Use `searchTools(\"query\")` inside the script to get a tool's input schema before calling it, and return only what you need.\n")
	for i, tool := range tools {
		if i == mcpCodeModeCatalogLimit {
			fmt.Fprintf(&b, "- … %d more; find them with searchTools\n", len(tools)-i)
			break
		}
		fmt.Fprintf(&b, "- %s", tool.Name)
		if summary := firstSentence(tool.Description, 120); summary != "" {
			b.WriteString(": " + summary)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func firstSentence(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if i := strings.Index(text, ". "); i >= 0 {
		text = text[:i+1]
	}
	if len(text) > limit {
		cut := limit
		for cut > 0 && (text[cut]&0xC0) == 0x80 {
			cut--
		}
		text = strings.TrimSpace(text[:cut]) + "…"
	}
	return text
}
