# Mobile Account And Conversations

Operate mode: help a self-hosted AT user select a deployment, check public
capabilities, authorize in their system browser, and manage the resulting mobile
session, then continue persistent personal text conversations. Scope and
product facts come from the approved implementation brief; no new brand assumed.

Inherits `_ui/src/style/global.css`: charcoal `#161618`, text `#e8e6e3`, green
`#55e870`. Light mode uses `#fafafa`, `#1e1e20`, and a darker `#006d1b` action
green for readable contrast. System theme follows the user's ambient preference.
Native Material 3 typography and controls serve this operational task.

The connection screen puts one full-width URL field and one primary verification
action beneath a short introduction, followed by the public-versus-login boundary.
The result screen shows the canonical server first, then stacked capability rows
and a handoff blocker only for older/disabled servers. Compatible deployments
show a browser sign-in action with an explicit remember duration choice. Account
identity appears only after bearer /auth/me verification; expiry and sign-out
actions follow it. No dashboard-shaped placeholder content or admin-management
affordances are shown for non-admin users.

Administrators enter Conversations from Account. The list is a chronological
index by creation ID with model labels and native rename/delete menus, not a
dashboard. A dedicated creation form uses the safe catalog and optional system
instructions. Detail views use a plain, selectable transcript with speaker/model
labels and explicit persisted status. The composer remains below it, constrained
by available layout height so both keyboard and 200% text remain scrollable.
There are no image/tool placeholders or fake assistant responses.
Message history follows the server's per-conversation sequence, not replica ULID
or timestamp order. Older history loads two messages at a time so maximum-size
escaped replies fit within a bounded network response.

Recovery is visible: provisional streaming text never implies persistence, EOF
never implies completion, unknown admission offers **Recover same send**, and
active snapshots show bounded status checking with a cancellation action. Leaving
the detail view disconnects generation; saved partial history remains on the
server. User text is not logged, rendered as HTML, or copied to device preferences.

Content is constrained to 560 logical pixels, padded by 24, and scrolls inside
safe areas. Text scales without fixed-height rows. Buttons have 48+ logical-pixel
targets. State is communicated in words, not green alone. Standard native route
transitions and progress indications avoid decorative motion.
Conversation lists/transcripts use a wider 760-pixel maximum and 20-pixel padding
for long text, while forms retain the 560-pixel measure. Transcript items are
lazily rendered. Controls use text plus icons and never color-only status.

Source and widget review completed in-thread (no subagent tool available). Tests
cover both themes and phone/tablet layouts at 200% text, long replies and keyboard
insets. No HTML/CSS detector was
run because this is native Flutter. Native visual review is blocked by missing
Android SDK/emulator and macOS/Xcode; do not treat layout tests as screenshot or
screen-reader approval. Generated raster icons originate from Flutter 3.47.2
templates and remain provisional packaging artwork.
