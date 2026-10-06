# _ui — Svelte Frontend

## Purpose

Single-page admin UI for managing providers, workflows, tokens, skills, and chat. Built with Svelte 5, Vite, TailwindCSS 4.

## Stack

- **Framework**: Svelte 5 (`^5.57.1`)
- **Bundler**: Vite 8 (Rolldown; chunk groups use `build.rolldownOptions.output.codeSplitting`)
- **Router**: svelte-spa-router 5 (client-side hash routing; read `router.location` / `router.querystring`, not the removed v4 stores)
- **Styling**: TailwindCSS 4, lucide-svelte icons
- **HTTP**: axios with `baseURL: 'api/v1'` (relative, same-origin)
- **State**: kaykay `$state()` macro for reactive global stores
- **Package manager**: pnpm 12 (pinned via `packageManager` in package.json)

## Directory Layout

```
src/
  main.ts              → app bootstrap (mounts App)
  App.svelte           → layout shell: Sidebar + Navbar + Toast + Router
  routes.ts            → route map (path → component), single source of truth
  pages/               → page components (one per route)
  lib/
    api/               → axios wrappers per domain (gateway.ts, providers.ts, workflows.ts, ...)
    components/        → reusable UI (Sidebar, Navbar, Toast, ChatPanel)
    components/workflow/ → 19 workflow node editor components (one per node type)
    store/             → global stores (store.svelte.ts, toast.svelte.ts)
    helper/            → utilities (chat, codec, config snippets)
  style/               → global.css (tailwind)
```

## Pages (routes.ts)

Page components are dynamically imported through `lazy()`/`guarded()` in
`routes.ts`. Guards run before downloading a page. `route-loader.ts` coalesces
in-flight imports and caches successful modules; failed downloads show an
explicit reload screen and are not cached. Keep one stable wrapped route for
Chats (`/chats/:id?`) so changing conversation parameters never remounts an
active turn. Loading/error placeholders must stay lightweight and must not
import page components. Regression: `tests/route-loader.test.mjs`.

`/` Home, `/providers`, `/workflows`, `/workflows/:id` WorkflowEditor, `/chats/:id?` Chats (`pages/Chat.svelte`), `/sessions`, `/tokens`, `/secrets`, `/skills`, `/node-configs`, `/runs`, `/settings`, `/docs`, `*` NotFound

Chats is a single route with an optional param: `/chats` is an
unsaved scratch buffer and `/chats/:id` opens a saved conversation. Two
separate route entries would remount the page on navigation and kill the
in-flight turn, so they must stay merged.

Chats workbench setups are normalized and compared through
`lib/helper/chat-tool-selections.ts`. Fresh accounts start with `whoami`;
saved empty selections remain empty. New chat restores the entire account
setup, while opening saved history never changes account defaults. Discovery
(`discoverTools`) is separate from deliberate selection changes (`refreshTools`),
which capture a defaults snapshot before debouncing. Navigation and destruction
flush that snapshot and clear its timer. Regression coverage:
`tests/chat-workbench.test.mjs`, `tests/chat-workbench-lifecycle.test.mjs`,
`tests/debounced-save.test.mjs` and the preset write cases in
`tests/playground.test.mjs`.

The browser chat loop owns one turn from send/retry through lazy creation,
question persistence, all model/tool steps and final persistence. Its lifecycle
and iterative budget live in `lib/helper/chat-turn.ts`; navigation invalidates
the generation, and late callbacks/cleanup may not mutate the next turn. Model,
prompt, tools, routing and trace ID are snapshotted once per turn. Chats uses
`requireComplete` streaming and `chat-tools.ts` dispatches only JSON-object
arguments. `chat-persistence.ts` snapshots transcripts before attachment uploads
and serializes appends, while `serial-queue.ts` also orders defaults writes.
Regressions: `tests/chat-runtime.test.mjs`, `tests/chat-turn.test.mjs`,
`tests/chat-persistence.test.mjs` and `tests/chat-stream.test.mjs`.

Sending while a turn runs **queues** the message (`lib/helper/chat-queue.ts`)
instead of refusing it. The running turn takes everything queued at its next
step boundary: after tool results (before the next model call), after the
background-skill wait, or once the model has answered, in which case the same
turn continues with one more model call on the same trace. Queued messages
become ordinary user messages, persisted before the call that reads them.
Stop (button or Esc) and a failed turn leave the queue waiting; it is sent on
**Send now** or together with the next message, never silently dropped.
Navigation discards it with the rest of the buffer. Entries can be edited
(moved back into the composer, also ↑ on an empty composer) or removed.

**Interrupt & send** (queue header button, or Ctrl/Cmd+Enter while running)
does not wait for the step boundary: it queues the composer text, aborts the
running step and immediately starts a new turn (new trace) with the queue —
OpenCode's "interrupt and continue". Partial assistant text is kept. Before
that turn's first model call, `repairInterruptedTail` closes what the abort
left open: every unanswered tool call of the last assistant step gets an
explicit `INTERRUPTED_TOOL_RESULT` (providers reject unpaired calls, and the
model is told the tool may or may not have taken effect), and an empty unsaved
assistant placeholder is dropped. A tool result arriving after the abort is
discarded. Abort cannot undo a side effect a tool already caused.
Regressions: the queue cases in `tests/chat-runtime.test.mjs`,
`tests/chat-queue.test.mjs`.

`components/playground/MessageContent.svelte` owns text/Markdown/raw and media
presentation, with the selected workspace passed explicitly for media URLs.
It never runs tools, persists history or owns turn state. `chat-media-cache.ts`
owns the per-workbench upload/download cache; in-flight requests are shared,
successful uploads seed replay bytes, and failures remain retryable. Never make
this cache global across accounts/workspaces. Regressions:
`tests/chat-message-content.test.mjs`, `tests/chat-media-cache.test.mjs`.

### Weak-network behavior

Sessions turn state lives in `lib/helper/session-turn.ts`, separate from
transcript loading and composer/session creation. The controller owns running,
confirmation, finishing and terminal states; reset/Stop/destruction invalidate
callbacks before aborting the transport. History adoption clears received text
only when the saved response is present. Confirmation is single-flight and a
failed request leaves the prompt available for retry. Late history/confirmation
responses cannot mutate a replacement turn. Regression: `tests/session-turn.test.mjs`.

A background session/workspace probe failing with a transport error must keep
the ready shell mounted; only actual authentication/admission failures replace
it. Probes pause while hidden/offline and have a 15s read deadline. Concurrent
write preflights share one in-flight check, not a cached authorization decision.

Chats assigns stable `client_id`s to pending messages (migration 88 deduplicates
appends under the conversation lock). Saved messages are sent to completions as
`{at_message_id}` with `at_conversation_id`; the server resolves exactly those
owner-scoped rows and selected-workspace media. Unsaved messages stay inline.
If persistence omitted an attachment, the live message also stays inline rather
than replacing bytes the user still has with an omission notice. Missing/stale
references fail explicitly; they never silently shorten model context.

Sessions polls only `after_id` changes in bounded pages. `adaptive-poll.ts`
serializes polls, backs off from 3s to 30s, pauses hidden/offline and wakes on
reconnection/visibility. A removed cursor returns 409 and reloads history.
Manual refresh still performs a full read. Chats initially renders 50 messages;
Load older messages prepends another page without moving the scroll position.
`at_history_before` asks the server for the unseen prefix, so lazy rendering
never silently shortens model context. Unavailable or oversized history fails
explicitly rather than truncating it.

Chats model streams and Sessions turns use `X-AT-Stream-ID` to start one
execution. `resumable-stream.ts` reconnects only with GET and an exact byte
offset, including partial UTF-8/SSE frames. A disconnect does not cancel the
server's execution; Stop/navigation sends DELETE. Replay reads/cancellation
recheck account, workspace, feature/capability and (for Sessions) ownership.
Streams have 15s heartbeats and a 45s client silence watchdog. Header waits
are bounded at 30s and reconnect attempts back off. Detached execution is
bounded at 30 minutes and also inherits server shutdown.

Replay is **replica-local, not durable across restarts**: at most 32 streams,
8 MiB each, completed records retained up to 10 minutes (oldest completed
records may be evicted for capacity). Multiple replicas require sticky routing.
Missing/expired replay fails without starting new work. Check saved messages is
a safe GET; Retry starts a new turn and can repeat tool effects. Never add blind
POST retries or interpret a missing replay as permission to relaunch a turn.
Regressions: `tests/network-resilience.test.mjs`, `chat-network_test.go` in server
and postgres, and `tests/session-transport.test.mjs`.
Replay regressions: `tests/resumable-stream.test.mjs`,
`tests/chat-history-loading.test.mjs`, `internal/server/chat-streams_test.go`.

## Patterns

- **API layer**: each `lib/api/*.ts` creates axios instance, exports typed async functions. No generated OpenAPI client.
- **State**: import `$state`-based store objects, read/mutate directly. Example: `storeNavbar`, `storeToast`.
- **Toast**: `addToast(msg)` / `removeToast(id)` via `lib/store/toast.svelte.ts`
- **Workflow editor**: components in `lib/components/workflow/` — one Svelte component per node type, matching backend node registry
- **Workflow step details**: double-clicking a node (or Enter on the selected node) opens `NodeDetailsModal` (n8n-style: input | parameters/settings | output; tabs below `lg`). Single click only selects, so moving/deleting a step never pops a dialog; a canvas hint names the gesture while one step is selected. Adding a step (palette click, drag or "add next step") likewise only selects it; the dialog opens when the reader asks for it. **Execute step** runs the node plus required upstream steps with the dialog open; before the node has run, the input column previews upstream outputs. Closing (Esc/X/backdrop) applies edits; invalid edits keep it open.
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
