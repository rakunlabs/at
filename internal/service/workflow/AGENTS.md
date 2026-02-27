# internal/service/workflow — Workflow Engine

## Purpose

DAG-based workflow execution engine. Parses workflow graphs, topologically sorts nodes, runs them concurrently with fan-out/selection routing. Inspired by worldline-go/chore.

## Key Files

- `engine.go` — `Engine.Run()`: reachableNodes → parseGraph → topoSort → execute. Handles concurrent fan-out branches, early output channel for sync API responses
- `node.go` — Core interfaces: `Noder`, `NodeResult`, `NodeResultSelection`, `NodeResultFanOut`, `Registry`, lookup function types
- `scheduler.go` — Cron scheduler: loads enabled cron triggers, builds hardloop jobs, calls `Engine.Run` per tick. Supports `Reload()` for dynamic trigger changes
- `handler.go` — `ExecuteJSHandler` and `ExecuteBashHandler` — shared helpers for nodes that run JS or shell
- `goja.go` (507 lines) — Goja JS VM setup: registers helpers (toString, jsonParse, btoa/atob, getVar, httpGet/Post), BodyWrapper for request bodies

## Execution Model

```
Engine.Run(ctx, graph, inputs)
  → reachableNodes(graph)     // prune unreachable nodes
  → parseGraph(nodes, edges)  // create Noder instances via factory, validate each
  → topoSort(nodeStates)      // dependency-ordered execution list
  → for each node in order:
      gatherInputs(node)      // collect upstream outputs
      node.Run(ctx, reg, inputs) → NodeResult
        → NodeResult           // pass data to all downstream
        → NodeResultSelection  // route to specific output ports by index
        → NodeResultFanOut     // spawn goroutine per item on port 0
```

## Registry

`Registry` holds shared state during execution:
- `ProviderLookup func(key) (LLMProvider, defaultModel, error)`
- `SkillLookup func(nameOrID) (*Skill, error)`
- `VarLookup func(key) (string, error)` / `VarLister func() (map[string]string, error)`
- `NodeConfigLookup func(id) (*NodeConfig, error)`
- `RunInputs map[string]any` — original trigger inputs
- Thread-safe error collection and output aggregation

## Node Contract

```go
type Noder interface {
    Type() string
    Validate(ctx context.Context, reg *Registry) error
    Run(ctx context.Context, reg *Registry, inputs map[string]any) (NodeResult, error)
}
```

Registration: `workflow.RegisterNodeType(typeName, factory)` called from `init()` in nodes/ package.

## Key Patterns

- `ErrStopBranch` — sentinel error to gracefully terminate a branch without propagating
- Fan-out: `runFanOutBranch` spawns goroutines per item with WaitGroup
- Early output: first Output node fires sends result to `earlyOutput` channel for sync API
- JS sandboxing: Goja VM with helpers; panics map to JS type errors (`vm.NewTypeError`)
- Bash handler: `/bin/sh -c` with variable injection from `VarLister`
