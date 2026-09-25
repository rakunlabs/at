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

`/` Home, `/providers`, `/workflows`, `/workflows/:id` WorkflowEditor, `/chats/:id?` Chats (`pages/Chat.svelte`), `/sessions`, `/tokens`, `/secrets`, `/skills`, `/node-configs`, `/runs`, `/settings`, `/docs`, `*` NotFound

Chats is a single route with an optional param: `/chats` is an
unsaved scratch buffer and `/chats/:id` opens a saved conversation. Two
separate route entries would remount the page on navigation and kill the
in-flight turn, so they must stay merged.

## Patterns

- **API layer**: each `lib/api/*.ts` creates axios instance, exports typed async functions. No generated OpenAPI client.
- **State**: import `$state`-based store objects, read/mutate directly. Example: `storeNavbar`, `storeToast`.
- **Toast**: `addToast(msg)` / `removeToast(id)` via `lib/store/toast.svelte.ts`
- **Workflow editor**: components in `lib/components/workflow/` — one Svelte component per node type, matching backend node registry
- **Workflow step details**: double-clicking a node (or Enter on the selected node) opens `NodeDetailsModal` (n8n-style: input | parameters/settings | output; tabs below `lg`). Single click only selects, so moving/deleting a step never pops a dialog; a canvas hint names the gesture while one step is selected. **Execute step** runs the node plus required upstream steps with the dialog open; before the node has run, the input column previews upstream outputs. Closing (Esc/X/backdrop) applies edits; invalid edits keep it open.
- **Skill folder preview**: `SkillFilesDialog` has Raw/Preview per file (markdown opens in Preview, other files in Raw; Preview shows unsaved edits). Skill files are untrusted (marketplace, imports, other members), so markdown renders through `helper/skill-markdown.ts` — raw HTML escaped, images as text, only http(s)/mailto links — never `md()`, which passes HTML through. SKILL.md frontmatter is shown as a key/value card (`splitFrontmatter`), and relative links to files in the folder open them in the dialog (`resolveSkillLink`, cannot escape the folder). Regression: `tests/skill-files.test.mjs`.
- **Skill edit form and the folder**: SKILL.md is generated from the skill record, so the edit form *is* SKILL.md. Editing shows a *Skill folder* panel listing SKILL.md and the resource files; opening the folder (or a file) with unsaved form edits saves them first, and folder changes refresh a clean form. `PUT /skills/{id}` replaces the record, so the form re-sends `version`/`author`/`license` it has no fields for — omitting them erased that frontmatter on every form update.
- **Build output**: `make build-ui` → moves `_ui/dist/` to `internal/server/dist/` for Go embedding

## Interaction style

UI state changes are immediate: do not add Tailwind transition utilities, CSS
transitions, Svelte enter/exit transitions, animated reordering or smooth scrolling.
Hover/focus colors still change, without interpolation. Theme changes directly
toggle the dark class; no temporary transition-suppression mechanism is needed.

## Terminal display settings

`pages/Terminal.svelte` + `lib/components/HostTerminal.svelte` keep the terminal
palette independent of the page theme (default dark, `system` opts back into
following it) and let the owner choose a font family and size. All three are
stored in the existing `terminal_preferences` JSON blob, so there is no
migration; the backend validates them in `TerminalPreferencesAPI`.

`style/fonts/JetBrainsMonoNerdFontMono-{Regular,Bold}.woff2` (~1 MB each,
SIL OFL 1.1, licence served at `fonts/OFL.txt`) are bundled because a font must
be installed on the *device running the browser* — a phone cannot install one at
all, and neither can a locked-down desktop. The `@font-face` declarations in
`style/global.css` cost nothing until something renders with the family, so only
the users who select it pay the download. It is deliberately not the default.
The *Mono* variant is required: its icons are single-cell, so terminal columns
stay aligned. `HostTerminal` awaits `document.fonts.load()` and refits, because
xterm measures the cell before a web font arrives and would otherwise keep a
grid sized for the fallback.

The touch key row (`HostTerminal`) sends Esc, Tab, arrows, Home/End/PgUp/PgDn and
a sticky Ctrl, because a soft keyboard has none of them — without it a phone
cannot interrupt a process, complete a path or leave an editor. Ctrl folds into
the next character in `onData` rather than reading keydown, which mobile
keyboards report inconsistently, and arrows respect
`modes.applicationCursorKeysMode` (SS3 in editors, CSI otherwise). Buttons act on
`pointerdown` with `preventDefault` so the terminal keeps focus and the soft
keyboard stays open; the `click` handler only runs for keyboard activation
(`detail === 0`). The row lives inside the terminal frame so the fit addon
reserves its height and the host gets the smaller row count. The `key_bar`
preference is `auto` (touch devices only), `on` or `off`, since one account can
be used from both a phone and a desktop.

Full screen renders the terminal alone in a fixed overlay. Escape belongs to the
shell (editors and pagers need it), so exit is a floating button plus
Ctrl/Cmd+Shift+F, captured on `window` before xterm sees it. On `pointer: coarse`
devices the button never fades — there is no hover to bring it back and no
modifier keys on a phone keyboard, so fading it would trap the reader. The
floating group carries the copy control too, since full screen drops the toolbar.

**Copy selection** (`HostTerminal.copySelection`) exists because Ctrl+C is the
shell's interrupt: xterm cancels that keydown, so the browser never raises a
`copy` event and the selection is lost. Dragging selects as usual; this button
reads `Terminal.getSelection()` and writes it to the clipboard, then refocuses
the terminal. `navigator.clipboard` is absent when the terminal is reached over
plain HTTP on a LAN (not a secure context), so it falls back to a detached
textarea plus `document.execCommand('copy')` before reporting failure; the
failure toast points at right-click → Copy, which works through xterm's own
`contextmenu` handler. The button is never disabled — an empty selection answers
with a toast rather than a greyed-out control with no visible reason. Nothing is
added for Ctrl+Shift+C: it would have to be stolen from the pty for every user.

## Dev Workflow

```sh
make install-ui   # pnpm install
make run-ui       # vite dev (localhost:3000)
# Backend: make run in separate terminal
```
