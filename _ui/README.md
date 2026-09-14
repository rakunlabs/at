# AT web app

Svelte 5, TypeScript, Vite and TailwindCSS. The same UI runs in desktop browsers
and as an installed mobile PWA.

## Development

From this directory:

```sh
pnpm install
pnpm run dev
pnpm run check
node --test tests/*.test.mjs
pnpm run build
```

Vite runs on localhost:3000 and proxies API/auth requests to localhost:8080.
From the repository root, `make build-ui` builds and moves all public assets into
`internal/server/dist/` for Go embedding. No separate mobile build is needed.

## Shareable navigation

The app uses hash routes, including query parameters **after** the hash. These
selections survive refresh and respond to browser Back/Forward:

| Surface | Example |
| --- | --- |
| Files directory (workspace-relative) | `#/files?path=assets%2Fuploads` |
| Sessions conversation | `#/sessions?session=<id>` |
| Studio tab | `#/studio?tab=series` |
| Skills tab | `#/skills?tab=community` |
| MCP Sets tab | `#/mcps?tab=store` |
| Task detail tab | `#/tasks/<id>?tab=events` |

Defaults omit their parameter; unknown tab values display the default tab.
Files paths and session IDs are resolved under the currently selected workspace
and its normal access checks. A link does not select or grant access to a workspace.
Invalid directories display a load error rather than leaving the previous
directory's contents under the new URL. Existing Sessions links from task chats
retain `session` in the URL instead of consuming it on mount.

`src/lib/helper/route-query.ts` merges navigation changes with existing query
parameters; `route-choice.svelte.ts` provides validated, URL-owned tab selections.
Use replacement only for invalidating a deleted selection; ordinary navigation
adds history and selecting the same value does not add an entry.

Navigation audit: Playground conversations, workflow/organization/task detail
routes and Docs sections/guides already have URL identities. Remaining local-only
drilldowns include Files previews, Studio series/episode selection, and Traces
conversation/observation drawers; the table above documents this change's scope.

Regression checks: `node --test tests/route-query.test.mjs` covers query encoding,
merging, tab defaults and history behavior with a browser-history model. For a
browser integration check, enter nested folders and different sessions/tabs,
copy each URL into a fresh tab, refresh, and use Back/Forward. Also check missing
paths, workspace switching, and switching sessions during a streamed reply.

## Voice input

Playground and Sessions share the microphone's **Voice settings** control. Choose
OpenAI API, Browser dictation, Local Whisper (on the AT server), or Faster-Whisper
(on the AT server). The method/model preference is retained in this browser using
the existing `at-voice-method` / `at-voice-model` keys; browser dictation also
remembers `at-voice-language`, including Turkish (`tr-TR`).

Browser dictation uses `SpeechRecognition` / `webkitSpeechRecognition` where
available. It does not upload audio to AT's transcription API, but the browser
may use its own online speech service: local/offline operation is not guaranteed.
Unsupported browsers display the unavailable option without silently choosing
another method. OS keyboard dictation remains usable in the focused message field;
web apps cannot directly launch it as another microphone backend.

Final speech segments append to the editable draft, never send automatically.
Stop lets browser recognition finish its last result. Changing conversations,
leaving the page, or cancelling discards pending results and stops microphone
tracks/requests. `tests/browser-speech.test.mjs` covers final-result deduplication,
stop/cancel, unsupported browsers and speech-service errors.

## Mobile tables

`DataTable.svelte` keeps wide tables in a native horizontal scroll region while
search and pagination stay within the page. A hint appears only when columns
overflow, and the region is keyboard-focusable for arrow-key scrolling. Dashboard
model lists expand on tap rather than relying on hover text. The shell constrains
its grid column so the top bar cannot enlarge a phone's layout viewport. Verify
at 320/390px with long provider/model names, horizontal table scrolling, vertical
page scrolling, and wrapped pagination controls.

## Install on a phone or desktop

Serve AT over **HTTPS** (localhost is allowed for development). Open the normal
AT URL, sign in, then open **Settings → Install AT**.

- Chrome/Edge/Android: use Install AT when offered, or the browser’s Install app menu.
- iPhone/iPad: open in Safari → Share → Add to Home Screen → Open as Web App if offered.
- An installed app uses the normal web login. iOS may require signing in again
  when first opening the home-screen app.

The manifest uses relative scope, ID, start URL, icons and shortcuts so an
installation served under a URL prefix stays within that prefix. Serve the
directory URL with its trailing slash, just as for the existing relative API URLs.
Proxies must serve `manifest.webmanifest`, `workspace-media.js`, `offline.html`
and `icons/*` as static assets, rather than redirecting them to a login page.

## Connection and updates

AT requires a live server for authentication, conversations, files and workflows.
The existing workspace-media service worker also supplies a public offline page
when navigation to the app fails. In an already-open app, a connection banner is
shown when the browser reports it is offline. Reconnection retries a failed
initial session load; messages and mutations are never queued or replayed.

Only `offline.html` is placed in Cache Storage. API, authentication, chat and media
responses are never cached by the worker. Workspace media continues to use each
requesting tab’s workspace, including Range requests and cancellation on switch.

The application HTML and bundles load normally from the network. The worker checks
for updates on registration, returning to the app and reconnecting. Worker updates
activate without reloading an active chat or form; reload the app to load a new UI
release. Bump the worker’s offline cache version when changing `offline.html` so
the public fallback updates with the release. Cache cleanup is scoped to this
installation’s offline caches.

Installed app icons follow the browser/OS manifest update schedule. Android/Chrome
can refresh the icon after rechecking the manifest; this is not immediate and the
app cannot force it. Existing iOS home-screen icons may require removing and
re-adding the shortcut. Reloading the app updates its in-app logo separately.
The Go static handler sends `no-cache` for the manifest, worker, offline page and
favicon files so their stable URLs revalidate; HTML uses `no-store`. Keep those
headers intact through reverse proxies/CDNs. Changing `id` or `start_url` to force
an icon refresh would create a different app and should be avoided.

## Verification before release

After a production build, serve it via AT or `pnpm run preview` on localhost.
Check DevTools → Application for the manifest, 192/512px icons and active worker.
Load once online, then test offline reload and reconnect. API/auth/media failures
must never return the offline HTML. Test navigation open/close, workspace switching,
file previews and chat with the keyboard visible at phone and desktop sizes.
Check installation, safe areas, landscape and keyboard behavior on actual iOS and
Android devices; desktop emulation does not reproduce every standalone behavior.

## Brand assets

Repository-root `assets/` is the single source for the AT logo and favicon set.
`brand-assets.js` serves these files at relative `brand/` URLs in development and
emits them into the production build for Go embedding. Update the source files
there; no manual copying into `_ui/public` is needed. All supplied favicon sizes
are included, and the conventional `favicon.ico` URL uses the same source.

`BrandLogo.svelte` shares the SVG across navigation, sign-in, setup and the account
menu. The manifest uses the supplied 192px/512px PNGs, and Apple touch icons use
the 192px image. These are regular icons, not maskable artwork: the edge-to-edge
logo is not safe to crop as a maskable icon. The offline template embeds the same
SVG as a data URL during dev/build, so branding works without another cached
request. Bump the offline cache version when replacing that logo too.
