# Persistent administrator host terminals

`#/terminal` is an installation-administrator-only terminal for the Linux host
running AT. It is personal to the signed-in administrator, independent of the
selected workspace. Administrators can open up to 32 saved tabs, select an
interactive Linux account, remember a default account per host, rename/reorder
tabs, reconnect, and explicitly end a terminal.

The interface uses xterm.js. tmux is an implementation detail: each tab has its
own server/socket with the status bar and tmux command-prefix bindings disabled.
No personal `.tmux.conf` is loaded. AT does not inherit its process environment
into the attached tmux client. The shell uses the chosen account's home and
login shell; normal shell startup files and Linux permissions still apply.

## Host requirements

- Linux with a running systemd **system** manager.
- AT running as root, with access to systemd's manager and `/run`.
- `tmux` (3.2 or newer), `systemd-run`, `systemctl`, and `getent` in AT's PATH.
  On Debian/Ubuntu, install tmux with `apt-get install tmux` before restarting AT.
- A stable, unique `/etc/machine-id` on each host. Do not clone the same
  machine-id onto multiple hosts: it is the durable terminal target identity.
- PostgreSQL migration **52** is applied automatically during normal startup.

Host readiness is discovered when AT starts. Restart AT after installing missing
host dependencies. A sandboxed AT systemd unit must permit system-manager access,
user switching and the `/run/at-terminal-*` directories. AT never silently falls
back to a nonpersistent shell when the host cannot create the independent unit.

Each shell is started in an independent transient system service named
`at-terminal-<session-id>.service`, with `Type=forking`, `RemainAfterExit=yes`,
`GuessMainPID=no`, and `KillMode=control-group`. systemd assigns the selected Linux
user and supplemental groups. Its private runtime directory holds the tmux socket.
An exclusive host-level file lock serializes lifecycle operations across AT
processes on the same host. Ending a terminal stops its whole service cgroup.

## What persists

| Event | Result |
| --- | --- |
| Change tabs, leave the page, close the browser | The attachment closes; shells keep running. |
| Log out | Interactive access is revoked; saved tabs and shells remain. |
| Return/sign in again | Tabs and last active tab are loaded from PostgreSQL; the active tab reattaches. |
| Restart/update the AT systemd service | Independent terminal services survive; reconnect to attach again. |
| Connect from another browser | The new attachment takes control; the old tmux client detaches. |
| Restart the Linux host | Shells/processes end; saved tab metadata remains. Choose **Start shell if ended**. |
| Host is unavailable | Tab remains visible as offline; no fallback to another host. |
| **End terminal** | Explicitly stop the shell/processes, then remove its saved tab. |

Reattachment redraws the current terminal screen (including interactive programs).
xterm maintains a bounded 2,000-line scrollback for the current attachment; this
is not a persistent output archive. tmux maintains its own bounded history.
Past shell commands are never replayed automatically. An ambiguous startup timeout
keeps the saved row so a remotely created shell is not orphaned from the UI.

Tabs are per AT administrator; filesystem isolation is determined by the selected
**Linux** account. Two AT administrators selecting the same Linux account share
that account's filesystem permissions. A root shell is intentionally host-root
access, not a container or workspace sandbox.

## Multiple AT backends

All backends must use the same AT database and installation auth state. Existing
`server.alan` configuration is reused for DNS peer discovery and QUIC/TLS traffic.
For terminal routing, set `security.enabled: true` with the same nonempty secret
`security.key` on every peer. Use the existing configuration loader's secret
mechanism for this key; do not expose it to the browser. Without cluster admission,
the terminal works locally but remote terminal RPC handlers are not registered.

The load balancer needs ordinary WebSocket upgrades; sticky sessions are not
required. An ingress AT backend discovers the saved target, sends bounded
control/input RPCs through Alan, and receives a streamed, byte-exact PTY output
channel. Each stream is matched to an unpredictable connection ID and its expected
peer. Unknown/missing targets fail closed instead of opening a local shell.

HTTP and WebSocket admission require a live administrator session. The WebSocket
also requires an explicit allowed browser Origin. Both ingress and target validate
the session family/owner against persistent auth state. Input is revalidated, and
idle attachments recheck every ten seconds. Disconnect/revocation closes attached
clients, not the independent shell. Remote attachments have heartbeat expiry;
backpressure, input limits and write deadlines bound stalled connections.

## API

All paths below are relative to the configured base path and administrator-only:

- `GET/POST /api/v1/terminals` — list tabs/preferences/hosts, create a saved terminal.
- `GET /api/v1/terminals/targets/{target}/users` — interactive Linux accounts on that host.
- `PUT /api/v1/terminals/preferences` — personal active tab and default Linux users.
- `PUT /api/v1/terminals/{id}` — rename/reposition an owned tab.
- `DELETE /api/v1/terminals/{id}` — stop and delete an owned terminal.
- `POST /api/v1/terminals/{id}/start` — idempotently start an ended shell.
- `GET /api/v1/terminals/{id}/ws` — attach (binary PTY input/output; JSON resize/status).

Terminal content is not captured by LLM tracing. Lifecycle logs include owner,
terminal ID, target and selected Linux user, but not terminal input/output.

## Verification

```sh
# Real tmux/PTY tests; real Alan QUIC peers; native HTTP admission; PostgreSQL
# persistence, ownership and concurrent tab-limit tests.
AT_TEST_POSTGRES_DSN='postgres://postgres@localhost:5432/postgres?sslmode=disable' \
  go test -race -v ./internal/service/terminal ./internal/server ./internal/store/postgres -run '^TestTerminal'

# Explicitly opt in on a root/systemd TEST host. Creates and cleans up its own
# transient terminal unit; does not restart or modify the deployed AT service.
AT_TEST_HOST_TERMINAL=1 AT_TEST_TERMINAL_USER=root \
  go test -race -v ./internal/service/terminal -run '^TestTerminalSystemdLifecycle$'
```

The tmux/PTY test checks retained shell variables after detach/reattach, hidden
tmux chrome, and resize propagation to the inner shell's tty. The systemd test is
skipped unless explicitly enabled; PostgreSQL tests skip if its DSN is unavailable.

Before production rollout, run the opt-in systemd test on the deployment's Linux
image, then verify an actual AT service restart leaves an open tab's shell PID and
variables unchanged. Browser checks should cover creating two tabs, input, reload,
rename/reorder, reconnect and termination at desktop and phone widths.
