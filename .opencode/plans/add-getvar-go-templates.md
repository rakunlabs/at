# Plan: Add `getVar` Template Function to Go Templates

## Goal

Add a `getVar` function to Go templates so users can write `{{getVar "my_key"}}` in template, http_request, email, log, and exec nodes — matching the `getVar("my_key")` syntax already available in JavaScript nodes.

## Files to Change (8 files)

| # | File | Change |
|---|------|--------|
| 1 | `internal/render/render.go` | Add `ExecuteWithFuncs()` function |
| 2 | `internal/service/workflow/nodes/template.go` | Accept `reg`, pass `getVar` to templates |
| 3 | `internal/service/workflow/nodes/log.go` | Same |
| 4 | `internal/service/workflow/nodes/http-request.go` | Same — update `renderTemplate` + `Run` |
| 5 | `internal/service/workflow/nodes/email.go` | Same — update `renderEmailTemplate` + `Run` |
| 6 | `internal/service/workflow/nodes/exec.go` | Same — update `resolveTemplate` + `Run` |
| 7 | `internal/service/workflow/nodes/helpers.go` | New file: shared `varFuncMap(reg)` helper |
| 8 | `_ui/src/lib/components/workflow/ChatPanel.svelte` | Update system prompt variable docs |

---

## Change 1: `internal/render/render.go`

**Current file (9 lines):**
```go
package render

import (
    _ "github.com/rytsh/mugo/fstore/registry"
    "github.com/rytsh/mugo/render"
)

var ExecuteWithData = render.ExecuteWithData
```

**New file:**
```go
package render

import (
    "bytes"
    "log/slog"

    "github.com/rytsh/mugo/fstore"
    _ "github.com/rytsh/mugo/fstore/registry"
    "github.com/rytsh/mugo/render"
    "github.com/rytsh/mugo/templatex"
)

var ExecuteWithData = render.ExecuteWithData

// ExecuteWithFuncs renders a Go template with the standard mugo function map
// plus additional custom functions. Use this to inject per-execution functions
// like getVar that need access to runtime state.
func ExecuteWithFuncs(content string, data any, extraFuncs map[string]any) ([]byte, error) {
    tpl := templatex.New(
        templatex.WithAddFuncMapWithOpts(func(o templatex.Option) map[string]any {
            return fstore.FuncMap(
                fstore.WithLog(slog.Default()),
                fstore.WithTrust(true),
                fstore.WithExecuteTemplate(o.T),
            )
        }),
        templatex.WithAddFuncMap(extraFuncs),
    )

    var buf bytes.Buffer
    if err := tpl.Execute(
        templatex.WithIO(&buf),
        templatex.WithContent(content),
        templatex.WithData(data),
    ); err != nil {
        return nil, err
    }

    return buf.Bytes(), nil
}
```

---

## Change 2: `internal/service/workflow/nodes/helpers.go` (NEW FILE)

```go
package nodes

import (
    "github.com/rakunlabs/at/internal/service/workflow"
)

// varFuncMap builds a Go template FuncMap with a getVar function
// that resolves variables from the workflow registry.
func varFuncMap(reg *workflow.Registry) map[string]any {
    funcs := make(map[string]any)
    if reg != nil && reg.VarLookup != nil {
        funcs["getVar"] = func(key string) (string, error) {
            return reg.VarLookup(key)
        }
    }
    return funcs
}
```

---

## Change 3: `internal/service/workflow/nodes/template.go`

### Edit A: Change `Run` signature (line 52)

**Old:**
```go
func (n *templateNode) Run(_ context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
```

**New:**
```go
func (n *templateNode) Run(_ context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
```

### Edit B: Change render call (line 65)

**Old:**
```go
    result, err := render.ExecuteWithData(n.tmplText, ctx)
```

**New:**
```go
    result, err := render.ExecuteWithFuncs(n.tmplText, ctx, varFuncMap(reg))
```

---

## Change 4: `internal/service/workflow/nodes/log.go`

### Edit A: Change `Run` signature (line 63)

**Old:**
```go
func (n *logNode) Run(ctx context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
```

**New:**
```go
func (n *logNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
```

### Edit B: Change render call (line 77)

**Old:**
```go
        rendered, err := render.ExecuteWithData(n.message, tmplCtx)
```

**New:**
```go
        rendered, err := render.ExecuteWithFuncs(n.message, tmplCtx, varFuncMap(reg))
```

---

## Change 5: `internal/service/workflow/nodes/http-request.go`

### Edit A: Change `Run` signature (line 110)

**Old:**
```go
func (n *httpRequestNode) Run(ctx context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
```

**New:**
```go
func (n *httpRequestNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
```

### Edit B: Update all `renderTemplate` calls in `Run` to pass extra funcs

All calls to `renderTemplate("name", tmpl, tmplCtx)` become `renderTemplate("name", tmpl, tmplCtx, varFuncMap(reg))`.

There are calls for: url (line 118), method (line 124), body (around line 150), and headers (in a loop).

### Edit C: Update `renderTemplate` function signature (line 271)

**Old:**
```go
func renderTemplate(name, tmplText string, ctx map[string]any) (string, error) {
    result, err := render.ExecuteWithData(tmplText, ctx)
```

**New:**
```go
func renderTemplate(name, tmplText string, ctx map[string]any, funcs map[string]any) (string, error) {
    result, err := render.ExecuteWithFuncs(tmplText, ctx, funcs)
```

---

## Change 6: `internal/service/workflow/nodes/email.go`

### Edit A: Update all `renderEmailTemplate` calls in `Run` to pass extra funcs

`Run` already has `reg` (line 137). All calls to `renderEmailTemplate("name", tmpl, tmplCtx)` become `renderEmailTemplate("name", tmpl, tmplCtx, varFuncMap(reg))`.

### Edit B: Update `renderEmailTemplate` function signature (line 302)

**Old:**
```go
func renderEmailTemplate(name, tmplText string, ctx map[string]any) (string, error) {
    // ...
    result, err := render.ExecuteWithData(tmplText, ctx)
```

**New:**
```go
func renderEmailTemplate(name, tmplText string, ctx map[string]any, funcs map[string]any) (string, error) {
    // ...
    result, err := render.ExecuteWithFuncs(tmplText, ctx, funcs)
```

---

## Change 7: `internal/service/workflow/nodes/exec.go`

### Edit A: Change `Run` signature (line 122)

**Old:**
```go
func (n *execNode) Run(ctx context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
```

**New:**
```go
func (n *execNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
```

### Edit B: Update all `resolveTemplate` calls in `Run` to pass extra funcs

Calls to `resolveTemplate(tmpl, data)` become `resolveTemplate(tmpl, data, varFuncMap(reg))`.

### Edit C: Update `resolveTemplate` function signature (line 253)

**Old:**
```go
func resolveTemplate(s string, data map[string]any) string {
    result, err := render.ExecuteWithData(s, data)
```

**New:**
```go
func resolveTemplate(s string, data map[string]any, funcs map[string]any) string {
    result, err := render.ExecuteWithFuncs(s, data, funcs)
```

---

## Change 8: `_ui/src/lib/components/workflow/ChatPanel.svelte`

### Update system prompt variable access docs

**Old:**
```
Variables are accessed differently depending on context:
- In JavaScript nodes (script, conditional, loop): use getVar("key") function
- In Go template nodes (template, http_request, email, log, exec): variables must be resolved by an upstream script node using getVar() and passed as data; there is no direct getVar in Go templates
- In bash tool handlers (skills): available as $VAR_KEY environment variables (uppercase, dots/hyphens replaced with underscores)
```

**New:**
```
Variables are accessed differently depending on context:
- In JavaScript nodes (script, conditional, loop): use getVar("key") function
- In Go template nodes (template, http_request, email, log, exec): use {{getVar "key"}} template function
- In bash tool handlers (skills): available as $VAR_KEY environment variables (uppercase, dots/hyphens replaced with underscores)
```

---

## Verification

1. `make test` — run all Go tests
2. `make lint` — run golangci-lint
3. `cd _ui && pnpm svelte-check` — verify frontend
4. Manual test: create a workflow with a template node using `{{getVar "some_key"}}` and verify it resolves

---

## Notes

- `ExecuteWithData` remains unchanged for backward compatibility (any code not needing extra funcs still uses it)
- Creating a `templatex.Template` per `ExecuteWithFuncs` call is acceptable — the global singleton already does Clone+Parse per call, and template rendering is not a hot path
- The `varFuncMap` helper returns an empty map (not nil) when VarLookup is nil, so `ExecuteWithFuncs` always gets a valid map
- `getVar` returns `(string, error)` — Go templates handle the error automatically (template execution stops on error)
