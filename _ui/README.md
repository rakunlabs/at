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

## Install on a phone or desktop

Serve AT over **HTTPS** (localhost is allowed for development). Open the normal
AT URL, sign in, then open the navigation menu → **Install AT**.

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

## Verification before release

After a production build, serve it via AT or `pnpm run preview` on localhost.
Check DevTools → Application for the manifest, 192/512px icons and active worker.
Load once online, then test offline reload and reconnect. API/auth/media failures
must never return the offline HTML. Test navigation open/close, workspace switching,
file previews and chat with the keyboard visible at phone and desktop sizes.
Check installation, safe areas, landscape and keyboard behavior on actual iOS and
Android devices; desktop emulation does not reproduce every standalone behavior.

App icons are generated from `public/icons/icon.svg` with FFmpeg, for example:
`ffmpeg -i public/icons/icon.svg -vf scale=192:192 -frames:v 1 public/icons/icon-192.png`.
Use 512 for the large/maskable icon and 180 for `apple-touch-icon.png`.
