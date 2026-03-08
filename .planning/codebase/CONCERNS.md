# Codebase Concerns

**Analysis Date:** 2026-03-08

## Tech Debt

**Massive God-Struct: Server (40+ store fields):**
- Issue: The `Server` struct in `internal/server/server.go` holds 40+ store interface fields, each passed individually through the constructor. The `New()` function signature on line 294 spans a single line with 45+ parameters. This makes it extremely fragile to extend and painful to test.
- Files: `internal/server/server.go` (lines 50–233, 293–355)
- Impact: Every new store interface requires modifying the `Server` struct, `New()` signature, `store.StorerClose` interface, and every call site. High coupling, impossible to unit test the server without mocking 40+ interfaces.
- Fix approach: Introduce an `Options` or `Dependencies` struct to bundle store interfaces. Example:
  ```go
  type ServerDeps struct {
      Store           service.ProviderStorer
      TokenStore      service.APITokenStorer
      // ...
  }
  func New(ctx context.Context, cfg config.Server, deps ServerDeps) (*Server, error)
  ```

**Massive God-Functions: NewEngine and NewScheduler (24+ parameters):**
- Issue: `workflow.NewEngine()` (`internal/service/workflow/engine.go:65`) takes 24 positional parameters. `workflow.NewScheduler()` (`internal/service/workflow/scheduler.go:73`) takes 25. Every call site duplicates all parameters.
- Files: `internal/service/workflow/engine.go:65`, `internal/service/workflow/scheduler.go:73`, `internal/server/workflows.go:446`, `internal/server/triggers.go:529`, `internal/server/builtin-tools-workflow.go:493`
- Impact: Adding a new lookup function requires updating 5+ call sites. Parameter ordering bugs are invisible until runtime. Extremely hard to read and review.
- Fix approach: Use an `EngineConfig` struct with named fields. Pass struct instead of 24 positional params.

**Giant Domain Types File (1443 lines, 40 interfaces):**
- Issue: `internal/service/at.go` contains ALL domain types and ALL 40 store interfaces in a single 1443-line file. Each new feature adds more interfaces here.
- Files: `internal/service/at.go`
- Impact: Merge conflicts when multiple developers touch this file. Cognitive overload. Hard to navigate.
- Fix approach: Split into focused files: `provider.go`, `workflow.go`, `agent.go`, `rag.go`, etc. Keep the package name but organize types by domain.

**Monolithic Server Package (59 files, 21K lines):**
- Issue: `internal/server/` has 59 Go files totaling 21,223 lines. Every HTTP handler lives in this one package, from gateway to RAG to bots to marketplace.
- Files: `internal/server/` (all files)
- Impact: Long compile times for the package, difficulty understanding boundaries, high risk of unintended coupling between handlers. The largest files are `gateway-rag-mcp.go` (1351 lines), `server.go` (1207 lines), `builtin-tools.go` (1089 lines).
- Fix approach: Extract coherent sub-packages: `server/gateway`, `server/rag`, `server/bot`, `server/admin`. Each with its own handler set and minimal interface to the core server.

**Misspelled Package Name: `antropic`:**
- Issue: The Anthropic provider package is named `antropic` (missing 'h') at `internal/service/llm/antropic/`. This is a permanent typo baked into import paths throughout the codebase.
- Files: `internal/service/llm/antropic/`, `internal/server/server.go:20`, `internal/server/auth-device.go:18`, `cmd/at/main.go:17`
- Impact: Confusing for new contributors. Inconsistent with external API naming (`providerType: "anthropic"` in ProviderInfo). Cannot be fixed without a coordinated rename across all importers.
- Fix approach: Rename package to `anthropic`. Update all imports. One commit, one PR.

**Store Backend Synchronization Burden (3 backends, 47 migrations):**
- Issue: Every store interface must be implemented across 3 backends (postgres, sqlite3, memory). The postgres and sqlite3 backends have 47 migration files each, and the memory backend must manually replicate all logic. Files differ slightly between backends (postgres has `migrate.go`, `secrets.go`, `skills.go`, etc. that memory handles in `memory.go`).
- Files: `internal/store/postgres/` (8901 lines), `internal/store/sqlite3/` (8766 lines), `internal/store/memory/` (4357 lines)
- Impact: Adding a new entity requires creating: interface in `at.go`, postgres implementation + migration, sqlite3 implementation + migration, memory implementation. 4+ files minimum. Bugs can silently exist in one backend only.
- Fix approach: Consider using sqlc or a shared query builder to reduce per-backend duplication. Alternatively, generate the memory store from interface definitions.

## Known Bugs

**`context.Background()` Used Where Request Context Should Be:**
- Symptoms: Operations in `internal/server/gateway-mcp.go` (lines 146, 163, 168, 181, 186, 418, 647) use `context.Background()` instead of the request context, meaning these operations won't be cancelled when the HTTP request is cancelled.
- Files: `internal/server/gateway-mcp.go`, `internal/server/skills.go:487,504`, `internal/server/builtin-tools-workflow.go:435,442,450,460,475`
- Trigger: Client disconnects mid-request; operations continue consuming resources.
- Workaround: These are within request handlers so the impact is limited to wasted work, not data corruption. But for long-running MCP tool calls, this could accumulate.

**`tokenLastUsed` sync.Map Never Cleaned Up:**
- Symptoms: The `tokenLastUsed` and `tokenLastUsedMu` `sync.Map` fields on `Server` grow monotonically — entries are never deleted when tokens are revoked or expired.
- Files: `internal/server/server.go:191-195`, `internal/server/gateway.go:477-492`
- Trigger: Over time with many tokens, memory grows unboundedly.
- Workaround: Process restart clears the maps. Low practical impact unless thousands of unique tokens are used over the server's lifetime.

## Security Considerations

**Workflow Bash/Exec Nodes Execute Arbitrary Shell Commands:**
- Risk: The `exec` node (`internal/service/workflow/nodes/exec.go`) and `ExecuteBashHandler` (`internal/service/workflow/handler.go:96`) execute arbitrary shell commands. The exec node has sandbox path traversal protection, but the bash handler inherits the full parent process environment (`os.Environ()`), including potentially sensitive variables.
- Files: `internal/service/workflow/nodes/exec.go`, `internal/service/workflow/handler.go:88-146`
- Current mitigation: Exec node uses sandbox directory with path traversal checks (`isInsideSandbox`). Bash handler has a 60s default timeout. Both require workflow authoring access.
- Recommendations: (1) Filter sensitive env vars (DB passwords, API keys, encryption keys) before passing to `ExecuteBashHandler`. (2) Add configurable allow/deny lists for shell commands. (3) Consider running exec in a container or restricted namespace.

**Goja JS VM Has Unrestricted HTTP Access:**
- Risk: JavaScript running in workflow nodes (conditional, loop, script, agent_call tool handlers) can make arbitrary HTTP requests via `httpGet`, `httpPost`, `httpPut`, `httpDelete` helpers. There is no URL allowlist, no SSRF protection, and `io.ReadAll(resp.Body)` on line 428 of `goja.go` reads unbounded response bodies.
- Files: `internal/service/workflow/goja.go:357-439`
- Current mitigation: 30-second HTTP timeout (`httpTimeout`). Only workflow authors can define JS handlers.
- Recommendations: (1) Add `io.LimitReader` to cap response body size. (2) Consider URL allowlist/denylist for internal network protection (SSRF). (3) Add memory limits to the Goja VM.

**`InsecureSkipVerify` Available in Multiple Components:**
- Risk: TLS certificate verification can be disabled via config for every LLM provider, the RAG embedder, HTTP request workflow nodes, email nodes, and discovery endpoints. A compromised config could silently disable TLS verification.
- Files: `internal/service/rag/embedder.go:148`, `internal/service/llm/openai/auth.go:143-147`, `internal/service/llm/antropic/antropic.go:119-120`, `internal/service/llm/gemini/gemini.go:70-71`, `internal/service/llm/vertex/vertex.go:68-69`, `internal/service/workflow/nodes/http-request.go:230-231`, `internal/config/config.go:318-321`
- Current mitigation: Disabled by default (`false`). Must be explicitly set per provider/node.
- Recommendations: Log a warning when `InsecureSkipVerify` is enabled. Consider removing it or gating behind an explicit "insecure mode" flag.

**No Request Body Size Limits on Gateway Endpoint:**
- Risk: The `ChatCompletions` handler at `internal/server/gateway.go:97` does `json.NewDecoder(r.Body).Decode(&req)` without any body size limit. An attacker with a valid API token could send a very large request body to exhaust memory.
- Files: `internal/server/gateway.go:97`
- Current mitigation: The endpoint requires authentication. Other endpoints (outbound HTTP) do use `io.LimitReader`.
- Recommendations: Add `http.MaxBytesReader(w, r.Body, maxSize)` before decoding. Apply consistently to all 90+ `json.NewDecoder(r.Body).Decode` call sites.

**No Rate Limiting:**
- Risk: No rate limiting exists anywhere in the codebase. An authenticated user can make unlimited requests to the gateway, triggering unlimited upstream LLM API calls.
- Files: `internal/server/gateway.go` (entire handler chain)
- Current mitigation: None beyond external infrastructure (reverse proxy, load balancer).
- Recommendations: Add per-token rate limiting middleware. The `APIToken` model already has scope restrictions; add rate limit fields.

**CI Linting Has Exit Code 0 (Non-Blocking):**
- Risk: The CI pipeline at `.github/workflows/test.yml:23` runs `golangci-lint` with `--issues-exit-code=0`, meaning lint failures never block merges.
- Files: `.github/workflows/test.yml:23,26`
- Current mitigation: SonarCloud may catch issues downstream.
- Recommendations: Change `--issues-exit-code=0` to `--issues-exit-code=1` to make lint failures blocking.

## Performance Bottlenecks

**Memory Store Single Global Mutex:**
- Problem: The in-memory store uses a single `sync.RWMutex` for ALL operations across ALL entity types (providers, tokens, workflows, agents, etc. — 35+ maps).
- Files: `internal/store/memory/memory.go:22`
- Cause: Every read/write to any entity type contends on the same lock. A slow list operation on workflows blocks all token lookups.
- Improvement path: Use per-entity-type locks (one RWMutex per map). This is a straightforward refactor since each method only touches one entity type.

**Scheduler Full Restart on Any Trigger Change:**
- Problem: The cron scheduler stops and recreates the entire cron runner whenever any trigger is added, updated, or removed. For N triggers, this is O(N) work per change.
- Files: `internal/service/workflow/scheduler.go:1-8` (package doc), `internal/service/workflow/scheduler.go:66-69`
- Cause: The underlying `hardloop` library doesn't support dynamic job management.
- Improvement path: Evaluate cron libraries that support AddJob/RemoveJob (like `robfig/cron`). Or implement differential reload.

**NewEngine Created Per Workflow Execution:**
- Problem: A new `Engine` instance is created for every workflow run (via `workflow.NewEngine(...)` with 24 params). While the struct is lightweight, the repeated construction and parameter passing is wasteful.
- Files: `internal/server/workflows.go:446`, `internal/server/triggers.go:529`, `internal/server/builtin-tools-workflow.go:493`
- Cause: The engine holds no mutable state between runs; it's purely a holder for lookup functions.
- Improvement path: Create engine once and reuse. The lookup functions are closures over the server, so they're stable across runs.

## Fragile Areas

**Server Constructor Wiring (New function):**
- Files: `internal/server/server.go:293-355`, `cmd/at/main.go`
- Why fragile: The `New()` function has 45+ positional parameters. A misplaced parameter causes a silent type mismatch if types happen to match, or a compile error that requires re-reading the entire signature.
- Safe modification: Always match parameter names to struct field names. Add new stores at the end of the parameter list. Consider the `Dependencies` struct approach.
- Test coverage: No tests for server initialization.

**Store Interface Expansion:**
- Files: `internal/service/at.go`, `internal/store/store.go`, `internal/store/memory/memory.go`, `internal/store/postgres/postgres.go`, `internal/store/sqlite3/sqlite3.go`
- Why fragile: Adding a new store interface requires: (1) add interface to `at.go`, (2) add to `StorerClose` in `store.go`, (3) implement in all 3 backends, (4) add to `Server` struct, (5) add to `New()` params, (6) add migration for postgres and sqlite3. Missing any step causes compile errors that are non-obvious.
- Safe modification: Follow the exact pattern of existing stores. Grep for a recent store addition (e.g., `CostEventStorer`) and replicate every place it appears.
- Test coverage: No integration tests verifying all backends implement the same interface correctly.

**Workflow Node Registration (init() pattern):**
- Files: `internal/service/workflow/nodes/*.go` (21 files with `init()`)
- Why fragile: Nodes register via `init()` → `workflow.RegisterNodeType()`. If a node file has a compile error, it silently fails to register. The `register.go` file with blank imports is the registration trigger point, but there's no runtime verification that all expected nodes are registered.
- Safe modification: After adding a new node type, verify registration by checking `workflow.NodeTypes()` or equivalent. Add a test listing expected types.
- Test coverage: `internal/service/workflow/nodes/nodes_test.go` (850 lines) covers individual nodes but doesn't verify the complete registry.

**Bot Error Handling (14+ `nolint:errcheck`):**
- Files: `internal/server/bot-telegram.go` (14 `nolint:errcheck`), `internal/server/bot-discord.go` (16 `nolint:errcheck`)
- Why fragile: All bot `Send()` and `ChannelMessageSend()` calls ignore errors. If the bot platform rate-limits or rejects messages, the user gets no feedback and the system has no visibility into failures.
- Safe modification: At minimum, log send errors. Consider retry logic for transient failures.
- Test coverage: Zero test coverage for bot handlers.

## Scaling Limits

**In-Memory Store (No Persistence):**
- Current capacity: Works for development/single-session use.
- Limit: All data is lost on process restart. No replication, no backup, no persistence.
- Scaling path: Use postgres or sqlite3 for any production deployment. The in-memory store should be clearly documented as dev-only.

**sync.Map-Based Caches Grow Unboundedly:**
- Current capacity: Fine for typical usage patterns.
- Limit: `tokenLastUsed`, `tokenLastUsedMu`, `thoughtSigCache`, `activeRuns`, `pendingConfirmations` all use `sync.Map` without size bounds. Only `thoughtSigCache` has TTL-based cleanup (30-min sweep every 10 min). `tokenLastUsed`/`tokenLastUsedMu` entries are never deleted.
- Scaling path: Add periodic cleanup for all sync.Map caches. Consider bounded LRU caches for `tokenLastUsed`.

**SQLite Single-Writer Limitation:**
- Current capacity: Adequate for low-concurrency single-instance deployments.
- Limit: SQLite's WAL mode allows concurrent reads but only one writer at a time. Under high write load (many concurrent workflow executions recording results), writes will serialize.
- Scaling path: Use postgres for multi-instance or high-concurrency deployments.

## Dependencies at Risk

**No `golangci.yml` Configuration:**
- Risk: The CI downloads but doesn't enforce a golangci config (line commented out in `.github/workflows/test.yml:18-19`). Linting is effectively unenforced since `--issues-exit-code=0` is used.
- Impact: Code quality drift over time. New linting issues go undetected.
- Migration plan: Create `.golangci.yml`, enable key linters (errcheck, gosec, staticcheck), and set `--issues-exit-code=1`.

## Missing Critical Features

**No Rate Limiting / Throttling:**
- Problem: No rate limiting on any endpoint (gateway, API, webhooks).
- Blocks: Production deployment without an external rate-limiting proxy. A single compromised API token can exhaust upstream LLM budgets.

**No Request Body Size Enforcement:**
- Problem: Gateway endpoint and most API handlers decode request bodies without size limits.
- Blocks: Safe exposure to untrusted networks without additional infrastructure.

## Test Coverage Gaps

**Overall Coverage: 2,025 test lines / 59,711 total Go lines (3.4%):**
- What's not tested: The vast majority of the codebase. Only 6 test files exist:
  - `internal/crypto/crypto_test.go` (238 lines) — encryption/decryption
  - `internal/skillmd/parse_test.go` (114 lines) — skill markdown parsing
  - `internal/server/gateway-rag-mcp_test.go` (261 lines) — RAG MCP gateway
  - `internal/service/schema_test.go` (401 lines) — JSON schema sanitization
  - `internal/service/workflow/nodes/nodes_test.go` (850 lines) — workflow node logic
  - `internal/service/workflow/engine_test.go` (161 lines) — engine execution
- Files: Every file outside the above list has zero test coverage.
- Risk: Critical paths completely untested: gateway authentication, streaming, all store backends, provider adapters, bot handlers, scheduler, all CRUD handlers.
- Priority: High

**Server Package (21K lines, 0 tests for HTTP handlers):**
- What's not tested: All 59 handler files, authentication logic, middleware chain, provider hot-reload, workflow execution via HTTP.
- Files: `internal/server/gateway.go`, `internal/server/workflows.go`, `internal/server/triggers.go`, `internal/server/auth-device.go`, etc.
- Risk: Gateway is the primary entry point. Auth bypass, streaming bugs, and response format issues would go undetected.
- Priority: High

**Store Backends (22K lines across 3 backends, 0 tests):**
- What's not tested: All CRUD operations across postgres, sqlite3, and memory backends. Encryption/decryption of stored credentials. Migration correctness.
- Files: `internal/store/postgres/`, `internal/store/sqlite3/`, `internal/store/memory/`
- Risk: Data corruption, encryption failures, and migration issues would go undetected until production.
- Priority: High

**LLM Provider Adapters (0 tests):**
- What's not tested: Request translation to/from each provider API, streaming chunk parsing, error handling, token refresh flows.
- Files: `internal/service/llm/openai/openai.go` (483 lines), `internal/service/llm/antropic/antropic.go` (644 lines), `internal/service/llm/gemini/gemini.go` (1048 lines), `internal/service/llm/vertex/vertex.go` (451 lines)
- Risk: Provider API changes or response format changes break silently.
- Priority: Medium

---

*Concerns audit: 2026-03-08*
