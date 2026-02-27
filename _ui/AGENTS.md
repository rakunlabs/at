# _ui — Svelte Frontend

## Purpose

Single-page admin UI for managing providers, workflows, tokens, skills, and chat. Built with Svelte 5, Vite, TailwindCSS 4.

## Stack

- **Framework**: Svelte 5 (`^5.46.1`)
- **Bundler**: Vite 6
- **Router**: svelte-spa-router (client-side hash routing)
- **Styling**: TailwindCSS 4, lucide-svelte icons
- **HTTP**: axios with `baseURL: 'api/v1'` (relative, same-origin)
- **State**: kaykay `$state()` macro for reactive global stores
- **Package manager**: pnpm

## Directory Layout

```
src/
  main.ts              → app bootstrap (mounts App)
  App.svelte           → layout shell: Sidebar + Navbar + Toast + Router
  routes.ts            → route map (path → component), single source of truth
  pages/               → page components (one per route)
  lib/
    api/               → axios wrappers per domain (gateway.ts, providers.ts, workflows.ts, ...)
    components/        → reusable UI (Sidebar, Navbar, Toast, ChatPanel, SkillBuilderPanel)
    components/workflow/ → 19 workflow node editor components (one per node type)
    store/             → global stores (store.svelte.ts, toast.svelte.ts)
    helper/            → utilities (chat, codec, config snippets)
  style/               → global.css (tailwind)
```

## Pages (routes.ts)

`/` Home, `/providers`, `/workflows`, `/workflows/:id` WorkflowEditor, `/chat`, `/tokens`, `/secrets`, `/skills`, `/node-configs`, `/runs`, `/settings`, `/docs`, `*` NotFound

## Patterns

- **API layer**: each `lib/api/*.ts` creates axios instance, exports typed async functions. No generated OpenAPI client.
- **State**: import `$state`-based store objects, read/mutate directly. Example: `storeNavbar`, `storeToast`.
- **Toast**: `addToast(msg)` / `removeToast(id)` via `lib/store/toast.svelte.ts`
- **Workflow editor**: components in `lib/components/workflow/` — one Svelte component per node type, matching backend node registry
- **Build output**: `make build-ui` → moves `_ui/dist/` to `internal/server/dist/` for Go embedding

## Dev Workflow

```sh
make install-ui   # pnpm install
make run-ui       # vite dev (localhost:3000)
# Backend: make run in separate terminal
```
