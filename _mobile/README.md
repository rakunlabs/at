# AT Mobile

Flutter iOS/Android client for a user-selected, self-hosted AT server, with actual
browser PKCE login, authenticated account/session management, and persistent
personal text conversations for native administrators. Implements
`NATIVE_AUTH.md`'s **Mobile PKCE Handoff V1**, not connector OAuth or gateway keys.

## Implemented

- Enter an HTTPS server origin with an optional deployment base path.
- Verify public `GET <base>/auth/status`, then strictly validate its `mobile_auth`
  V1 descriptor against the selected issuer and every fixed endpoint.
- Begin S256 authorization and open the system browser for local password/passkey
  login and explicit approval. Exchange the returned code for a mobile session.
- Store credentials in OS secure storage and call bearer `GET <base>/auth/me`
  before showing the actual identity, subject, role, and session deadlines.
- Restore a saved session after the selected server is verified again, rotate
  expired access credentials, check the account on demand and on app resume,
  sign out, or sign out and switch servers. Non-admin identities are supported.
- Keep a public-only fallback for servers that do not advertise mobile auth.
- List, create, rename and delete owner-scoped conversations through the same
  Personal Chat V1 API used by web clients. Select from the safe model catalog,
  set initial system instructions, page through saved messages and stream replies.
- Explicitly cancel active replies and recover interrupted sends from durable
  snapshots. Non-admins see an authorization explanation, not chat controls.
- Remember only the last successfully verified canonical URL with `shared_preferences`;
  restore it as editable input, never as verified or signed-in state. Forget it on demand.
- System light/dark Material 3 themes, scrollable phone/tablet layouts, large text,
  accessible control labels, cancellation and safe retryable errors.

`lib/server.dart` owns URL normalization and bounded JSON transport.
`lib/auth_models.dart` owns PKCE, callback/discovery and token/identity validation.
`lib/auth.dart` owns browser/storage ports and serialized session lifecycle.
`lib/app.dart` owns server selection, account UI and themes; `lib/main.dart` is the
bootstrap. `lib/chat_models.dart`, `chat_stream.dart`, `chat.dart`, and
`chat_screens.dart` provide typed Personal Chat DTOs, SSE parsing/state, API and
recovery control, and conversation UI. No task/agent or provider-management
screens are fabricated.

Runtime packages: Dio 5.11.1, shared_preferences 2.5.5, crypto 3.0.7,
flutter_secure_storage 11.0.0, and flutter_web_auth_2 **6.0.0-alpha.7**. The latter
is a deliberate prerelease: its documented AGP-9/built-in-Kotlin support and
UIScene integration match Flutter 3.47's generated projects; stable 5.1.0's
Android Gradle integration is not AGP-9 compatible. Review/stabilize this plugin
before production distribution. `pubspec.lock` pins the exact graph.
Native Navigator and one session ChangeNotifier suffice; there is no app-level
deep-link router. Unsolicited callbacks never create a session.

## SDK Provenance

Isolated SDK: `/tmp/opencode/flutter` (temporary; may be removed by host cleanup).
The global `/home/ray/development/flutter` SDK was not upgraded or modified.

| Item | Value |
| --- | --- |
| Flutter stable | 3.47.2 |
| Dart | 3.13.2 |
| Framework revision | d3b14c876900e553bc736ca19295fc09e3853e8e |
| Official release date | 2026-08-27 |
| Release manifest | https://storage.googleapis.com/flutter_infra_release/releases/releases_linux.json |
| Archive | https://storage.googleapis.com/flutter_infra_release/releases/stable/linux/flutter_linux_3.47.2-stable.tar.xz |
| SHA-256 (verified before extraction) | `447878859d01ca9bfdb99a85f245af07ed8a15fedcd9d189c4749e8e92d1f185` |

The manifest's `current_release.stable` selected this exact release. Archive retained
at `/tmp/opencode/flutter_linux_3.47.2-stable.tar.xz`; manifest retained at
`/tmp/opencode/at-flutter-releases.json`. Download was directly from official Google
storage, with no third-party installer. For macOS, use the same release's official
macOS archive for your CPU, not this Linux SDK.

## Run And Verify

Run from `_mobile/`, always using the isolated executable (no global upgrade):

```sh
/tmp/opencode/flutter/bin/flutter pub get
/tmp/opencode/flutter/bin/flutter analyze
/tmp/opencode/flutter/bin/flutter test
/tmp/opencode/flutter/bin/flutter doctor -v
/tmp/opencode/flutter/bin/flutter run -d <device-id>
/tmp/opencode/flutter/bin/flutter build apk --debug
```

Android requires the Android SDK, command-line tools, platform/build-tools matching
Flutter's generated Gradle configuration, accepted SDK licenses, and a compatible
JDK (the project targets Java 17; the host has OpenJDK 21.0.3). Use current official
Android Studio's bundled JDK and SDK manager, set `ANDROID_HOME` for a custom SDK
location, and check `flutter doctor -v` before building. This host has **no detected
Android SDK or Android device**. `flutter build apk --debug` was attempted and
stopped with `No Android SDK found`; APK compilation/device rendering is unverified.
The app now compiles against **API 37** (required by secure_storage 11), with
minimum API 24, Java 17 target and AGP 9.1 built-in Kotlin enabled.

iOS requires macOS, a Flutter-compatible current Xcode with command-line tools and
iOS simulator runtimes, and an Apple development team/provisioning for physical
devices. Open `ios/Runner.xcworkspace`. Resolve generated Swift Package Manager
dependencies in Xcode; install CocoaPods if a future plugin requires it. On macOS
run `flutter build ios --simulator` and exercise iPhone/iPad simulators. Linux cannot
build or verify iOS; no iOS build/signing result is claimed here.
The generated iOS deployment target remains 15.0; the selected plugin's source
supports custom-scheme ASWebAuthenticationSession below 17.4 as well as the newer
callback API. Validate on the minimum OS as well as current iOS before release.

Analysis and 93 unit/widget tests passed on the isolated SDK. Regression tests
cover delayed preference writes, stale cancel callbacks, bounded chunked bodies
without Content-Length, byte-limit boundaries, invalid UTF-8, and rejection of
unfinished error/redirect bodies. PKCE tests cover the RFC vector, callback
swaps/duplicates/expiry/replay, malicious authorization destinations, issuer and
deadline checks, identity/family agreement, single-flight refresh, ambiguous
failure, failed secure writes, logout races and server isolation. Mocked browser
widgets cover cancellation, restoration, non-admin sign-in and switching servers.
Personal Chat tests cover CRUD, descending cursor pagination with chronological
display, two clients reloading the same mock durable transcript, Unicode byte
offsets, arbitrary SSE chunks, invalid sequences, missing terminal events,
same-ID explicit replay, bounded polling, cancellation, session invalidation,
mutation preflight and no automatic POST retry. Review regressions use real HTTP
for parent-token reuse after 409/503/timeouts, explicit shared-parent cancellation,
single-guard lifetime management, large Go-escaped SSE/recovery snapshots, bounded
two-message pages, and genuine wire overflow. Sequence tests cover inverted ULIDs,
positive-integer validation, delta preservation and immutable snapshot order.
Layout tests cover
320x568 phones and 834x1194 tablets at 200% text scale in both themes, including
long selectable transcript text and a simulated keyboard inset. These are not
native device screenshots or VoiceOver/TalkBack certification; native platform
rendering, keyboard behavior, screen-reader behavior, and release builds remain
device verification work.

## Security Contract

- HTTPS is mandatory by default. Embedded userinfo (even empty `@`), query,
  fragment, whitespace, traversal and ambiguous encoded path separators are rejected.
- Canonical URLs end in `/`. Relative `auth/status` and `#/` preserve the entire
  deployment base path. Never resolve an endpoint beginning with `/`.
- Every API redirect is rejected, including same-host redirects. Enter the final
  address explicitly. TLS validation is never disabled and app HTTP has no cookie
  jar. Begin/exchange/refresh use no authorization header; authenticated API calls send
  only the selected server's mobile access bearer. No gateway keys or passwords
  enter the app. The external browser has its own independent web session.
- Requests have a 15-second total deadline and cancellation, including while
  streaming the response. Error/redirect statuses are rejected before reading
  bodies. Authentication bodies are capped at 16,384 bytes while reading (even
  without Content-Length); overflow cancels the request before UTF-8/JSON decoding.
  Personal Chat uses the separately bounded response limits documented below.
  Each HTTP request/stream owns a child cancellation token. Deadlines, rejected
  responses and cleanup cancel only that child, never a caller's reusable lifetime
  token. Explicit parent cancellation cancels every currently linked child. A
  weakly keyed guard registers one callback per parent and removes children on
  completion, avoiding a retained callback for every poll on Dio's non-removable
  `whenCancel` future.
  Raw response bodies,
  redirect targets and exception details are not displayed or logged.
- The required booleans and `username-first` login mode are validated without
  coercion. V1 discovery pins issuer (canonical URL without trailing slash),
  callback, S256 and all endpoints. Unsupported descriptors fail closed.
- Status is a public snapshot, not proof of user identity, AT authenticity, or
  authorization. Disabled web auth does not unlock a mobile session.
- Preferences contain only the selected URL and a random nonsecret installation
  marker. Each credential key is scoped to that installation and SHA-256 of the
  exact canonical issuer. A token pair plus metadata is written as **one JSON
  secure-store value**, never split across independently committed keys. The write
  is read back before being considered committed. iOS uses non-synchronizing,
  unlocked-this-device Keychain items; Android uses the plugin's encrypted store.
- Android cloud backup and device transfer of app data are disabled/excluded.
  The installation marker prevents reuse of iOS Keychain entries surviving app
  reinstall; old entries become unreachable and are not enumerated/deleted across
  other installations. This-device Keychain items do not migrate to another device.
- Verifier and state are independent secure-random 32-byte canonical base64url
  values, held only in the pending in-memory attempt. Callback scheme/authority/
  path and parameters are checked before a fixed-length state comparison; no
  callback supplies a server/account. The exact approval URL must match the
  selected origin, base path, route, and sole canonical request_id.
- Token parsing requires exact `Bearer`, canonical independent tokens, issuer,
  family format, remember choice, and bounded access/absolute expiry. A one-minute
  future-clock tolerance applies to maximum lifetimes, never to already expired
  access/session deadlines. `/auth/me` must agree on subject, family, remember
  choice and both expiries. Refresh may not change the subject/family/deadline.
- Save and forget are serialized through explicit busy phases that disable URL
  editing and conflicting actions. Verification is cancellable only before the
  save commit begins; the non-cancellable write phase says "Saving server..." and
  removes Cancel. Forget blocks new input until removal finishes, so an older
  clear cannot overwrite a newer selection.

For explicit nonrelease development only,
`--dart-define=AT_ALLOW_LOCAL_HTTP=true` permits HTTP to exactly `localhost`,
`127.0.0.1`, or `::1`. Release builds ignore it. LAN addresses and Android's
`10.0.2.2` alias are not allowed. The flag does not disable platform cleartext/ATS
policy or TLS checks: prefer a trusted HTTPS development server. If a platform
blocks loopback HTTP, use HTTPS rather than adding a blanket network exception.
Loopback on a physical device refers to that device, not your workstation.

## Session Lifecycle

- One MobileSession owns all HTTP and storage mutations for the active server in
  the main isolate. Refresh is single-flight; authenticated GETs are serialized
  and only safe relative `auth/me` or `api/v1/...` paths are accepted. Never create
  parallel session owners/background refresh isolates without adding process-wide
  coordination. This app currently creates one owner and has no background worker.
  MobileSession is the app's auth coordinator; its `authenticatedRequest` and
  `authenticatedStream` helpers add conversation-specific path restrictions,
  cancellation tracking and verified `/auth/me` preflight before every mutation.
  Access with less than 30 seconds remaining is refreshed before mutation
  dispatch; a session with less than five seconds left is not used for a write.
  A resource GET's 401 only triggers rotation/retry if `/auth/me` also rejects
  access. Mutating POST/PATCH/DELETE calls are never automatically replayed.
- Before dispatching a refresh, the previous stored record is removed. This makes
  crashes or lost rotation responses fail closed instead of replaying a consumed
  refresh after restart. The response pair must be persisted before it becomes
  active. Storage failure removes the record (or writes a credential-free
  tombstone); if both fail, further sign-in is blocked until cleanup succeeds.
- Ambiguous refresh errors discard local credentials and require a new login.
  There is no automatic retry of the old refresh. A definite 429 preserves the
  unconsumed pair and enforces Retry-After before another request. There are no
  generic POST retries or automatic exchange retries. GET retries at most once
  after a 401-triggered rotation, within the same family.
- Logout invalidates in-flight results immediately, waits for serialized writes,
  removes local storage and sends the refresh-token logout form even if access
  has expired. Generation checks prevent delayed refresh/save resurrecting a
  signed-out session. Failed remote logout explicitly warns that the server
  session may still be active; it does not claim revocation. Switching servers
  performs this same cleanup and carries the result back to the selector.
- Remember is unchecked by default: standard sessions use server TTL (usually
  8 hours, at most 24 hours); extended sessions last up to 30 days. Both choices
  use secure storage and obey the server's immutable deadline. No cookie-lifetime
  semantics are inferred for the native client.

## Personal Conversations

Requires the **Personal Chat Backend V1** contract in root `PERSONAL_CHAT.md`,
migration 29, native auth and a live administrator identity. Conversations belong
to the current subject; being another administrator does not grant access to
them. Both mobile and web read the same server transcript. No browser-local
playground history is imported.

- The only catalog endpoint is `GET api/v1/conversations/models`. Its array is
  validated as exact provider_key/model pairs; no provider credentials or config
  endpoints are fetched. Creation sends title, provider_key, model and optional
  system_prompt. Rename sends only title. Delete requires explicit confirmation.
  Active-turn conflicts are shown rather than worked around.
- Conversation lists fetch 10 rows per page; message pages fetch **two** to bound
  worst-case escaped JSON. Both retain exclusive message/conversation-ID `before`
  cursors. Messages require the backend's positive integer **sequence**, scoped
  to the conversation. Pages arrive in descending sequence; the backend resolves
  the cursor ID to its sequence. History merges by durable ID and sorts by
  ascending sequence, never ULID or replica timestamps, for chronological rendering;
  **Load older messages** preserves newer loaded rows. A page is not represented
  as the complete transcript. Conversation ordering is descending ID, not activity.
  Deploy the backend sequence-field/order update together with this client;
  missing/invalid sequences are rejected rather than guessed from older IDs.
  A message's sequence cannot change in deltas, snapshots or subsequent pages.
- Each intentional send creates a secure-random UUID v4 and submits exactly
  `{content, request_id}`. Text is not trimmed or normalized. Only `accepted`
  introduces the durable user/assistant pair. Delta offsets count UTF-8 bytes,
  not Dart UTF-16 indices. Snapshot and terminal events replace provisional text.
  The UI distinguishes pending, generating, completed, failed and cancelled;
  length/content-filter finishes are shown without automatic retry.
- EOF without a terminal event, bad offsets/IDs, malformed frames and stream
  interruption trigger GET recovery. Known assistant IDs are polled every two
  seconds for at most six minutes per recovery attempt. Polling stops at terminal
  state, route disposal or auth invalidation. After the bound, the user can check
  again or cancel; the app never invents completion.
- If acceptance is unknown, the newest saved page is checked first. **Recover
  same send** is an explicit replay of the original ID and exact content, never
  an automatic POST. The pending tuple survives detail-page navigation within
  the Conversations route. It is memory-only and is cleared when leaving that
  route, logging out or switching sessions/servers. After process termination,
  reload the server transcript before deliberately sending a new turn; there is
  no persistent offline outbox or automatic replay on startup.
- Explicit cancellation sends the bodyless cancel POST and replaces local state
  with the returned snapshot, including when completion won the race. Leaving a
  detail screen disconnects its SSE and stops polling; the backend cancels upstream
  and saves the last durable partial text. No detached/background generation or
  reconnect subscription is implemented.
- Streams have a six-minute total bound and 30-second idle receive bound. Each
  SSE frame is limited to **8 MiB** before JSON parsing, with incremental UTF-8
  decoding. Assistant content is limited to 1 MiB UTF-8, user text/system
  instructions to 32 KiB, and titles/provider/model strings to 256 bytes. Catalog
  and small JSON responses are bounded to 256 KiB; conversation pages to 2 MiB;
  single message snapshots to **8 MiB**; two-message pages to **16 MiB + 64 KiB**
  for the page envelope. A raw text byte may expand to six bytes in Go JSON
  (`<` becomes `\u003c`, control characters become `\u00xx`). The 8 MiB bound
  accommodates the maximum 1 MiB assistant text, accepted user text and metadata,
  including worst-case escaping. The decoded 1 MiB text limit is unchanged;
  genuinely oversized wire frames/bodies or decoded content are still rejected.
- No transcript is written to preferences or secure storage. Loaded pages are
  scoped to one issuer/subject/session and discarded on exit or invalidation.
  Logout cancels active authenticated requests/streams before waiting for the
  auth mutation queue; generation checks reject stale results. Access rotation
  does not change the admitted stream's family or send a second generation POST.

Text is selectable and wraps using system light/dark Material 3 controls. There
is no Markdown/image/tool rendering, search, branching, attachments, transcript
editing, model-parameter controls, spending quota UI or offline synchronization.
Changing an existing conversation's model/instructions remains available through
the shared backend/web contract, not this mobile UI. Provider calls can incur
costs; the safe catalog does not guarantee that every configured model accepts
text. A mock durable backend validates client contracts, not PostgreSQL or a real
provider integration; deployed cross-client end-to-end verification remains a
separate acceptance check.

## Platform Handoff

Plugin setup was checked against the published package documentation and downloaded
source: https://pub.dev/packages/flutter_web_auth_2 and
https://pub.dev/packages/flutter_secure_storage.

Android declares the exported `com.linusu.flutter_web_auth_2.CallbackActivity`
with empty task affinity and a VIEW/DEFAULT/BROWSABLE intent filter for exactly
`atmobile://auth/callback`. iOS registers the `atmobile` URL scheme, uses the plugin's
ASWebAuthenticationSession/UIScene integration, and wires Keychain entitlements
for Debug/Profile/Release. Android uses a system authentication tab; no embedded
WebView is used. The production browser port rejects desktop/web platforms.

The callback scheme is the backend's fixed **provisional** V1 contract, not a
verified universal/app link. Another app can claim it, but cannot redeem the code
without the verifier. Do not change callback IDs independently of the server.
Cancellation uses browser Close/Back. A five-minute in-memory deadline rejects
late callbacks; the server additionally bounds code redemption to 60 seconds.

End-to-end use requires native auth enabled, migration 28 applied, and the browser
SPA's `#/mobile-authorize` approval route deployed under the same HTTPS base path.
This mobile change does not implement or change that separate browser/backend
work. Passwords/passkeys remain browser-owned; local password/passkey login is not
external OIDC. Physical iOS/Android browser return, Keychain/Keystore persistence,
reinstallation/backup policy, and a deployed TLS approval flow remain acceptance
tests. Mocked plugin ports are not proof of those native behaviors.

## Packaging

Generated with `flutter create --platforms=android,ios --org io.rakunlabs
--project-name at_mobile _mobile`. Android `io.rakunlabs.at_mobile` and iOS
`io.rakunlabs.atMobile` are **provisional generator identifiers, not approved
production signing IDs**. Confirm ownership, final IDs, signing teams/keys,
associated domains and redirect registration before distribution. Generated
Flutter launcher icons and launch artwork are placeholders from the official SDK,
not final AT app-store assets. The generated Android release configuration still
uses debug signing and must not be published as-is.

Build output, local SDK configuration, caches and signing material must not be
tracked. All app work is contained in `_mobile/`; no backend or root configuration
changes were made by this mobile implementation.
