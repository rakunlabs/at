# Developer Spaces on Kubernetes

## Status and scope

An experimental Kubernetes driver is available through the bootstrap
`server.sandbox` configuration. Docker remains the default. Kubernetes currently
requires **one AT replica** and explicit operator declarations for CNI enforcement
and kubelet PID limits. Those declarations are not automatic cluster validation.
Do not mount the node's Docker socket as a substitute for this backend.

Implemented: managed Pod/PVC reconciliation, UID-guarded pod deletion, persistent
home mounts/reset, namespace-scoped network policies, resource limits,
WebSocket/SPDY exec, terminal resize, a static init-image launcher/file helper,
command cancellation and runtime-aware UI warnings. Fake-client, local
launcher/PTY and opt-in real-cluster tests cover these contracts. The disposable
kind suite has passed on Kubernetes 1.35/linux-amd64 with its local-path storage:
Pod/PVC lifecycle, home isolation/reset, exec argument/environment/stdin/exit
semantics, file-helper delivery, command-child cancellation and terminal resize.
CNI enforcement, production CSI, multi-node storage and multi-architecture
images still require separate cluster testing.

**Not production-complete:** durable multi-replica sandbox ownership, activity
fencing, automatic owner-death recovery and replay coordination remain unfinished.
Developer session run exclusion and cancellation now have PostgreSQL receipts,
but these do not fence remote tool side effects or coordinate sandbox lifecycle. Enabling
`single_replica` acknowledges this deployment restriction; it does not enforce
the Deployment replica count. A namespace-wide controller Lease now rejects a
second process at sandbox admission, rather than trusting that declaration alone.

The first implementation step hardens Manager lifecycle handling: failed stop,
remove, purge and reconfiguration operations preserve local tracking and return
errors where the caller can handle them. Failed janitor/shutdown releases remain
tracked and are logged for retry. A successful home reset invalidates all local
mounts of that account's home, not just the initiating workspace.

None of these changes provides distributed coordination yet.

## Experimental single-replica setup

### Exclusive namespace ownership and recovery

The first sandbox operation claims `coordination.k8s.io` Lease
`at-sandbox-controller` in the dedicated sandbox namespace, with a random
per-process holder. Create/update use Kubernetes resource-version conflict
checks. A second process, including one with another deployment ID, cannot
manage that namespace while it is held. Every admitted operation checks the
holder and Lease UID; a five-second renewal loop also checks idle controllers.
Lease loss or an API-check failure permanently stops new operations and cancels
active exec/terminal contexts. Remote cancellation is best-effort during a
network outage; inability to confirm it leaves the Lease held for recovery.

Orderly shutdown cancels/drains operations and clears the holder only when
ownership is still valid. It never deletes PVCs as part of Lease release.
The example Role includes Lease create/get/update permissions; older controller
RBAC must be updated before sandbox admission works.

**No expiry-based takeover:** the 30-second duration and renewal timestamps are
diagnostic, not permission to steal ownership. A paused/partitioned controller
cannot be fenced merely by observing its expired timestamp. After a crash or
ownership loss, stop the former AT process (and ensure it cannot restart),
inspect/stop any orphan sandbox processes or Pods, and only then delete the
namespace's `at-sandbox-controller` Lease with an operator account. Restart AT
to claim it again. Do not delete an active controller's Lease to unblock a second
replica. An idle, cleanly released Lease may be reused by the same deployment;
switching deployment IDs requires the same deliberate recovery procedure.

After acquiring the Lease, a new controller stops this deployment's leftover
managed Pods **before accepting its first sandbox operation**. It does not
adopt potentially orphaned exec processes from a former controller. The pass
validates the managed Pod snapshot before deletion, uses UID preconditions,
waits for termination, and has a two-minute total deadline. A failed pass blocks
admission and is retried on the next request. Foreign workloads are untouched;
unrecognized Pods carrying this deployment's managed label block recovery for
operator inspection. PVCs and network policies are never deleted by this pass.

Shutdown now follows the reverse order: close admission, cancel/drain commands
and terminals, stop managed Pods (including leftovers not in the Manager's
local map), then release the Lease. Cleanup failure leaves the holder in place
and is reported. Consequently **an AT restart recreates sandbox root
filesystems**, not just an explicit Space Stop; workspace/home files survive in
PVCs. Prepare images with the required packages. An orphan cleanup pass only
runs after safe Lease acquisition; it does not bypass crashed-holder recovery.

This is a fail-closed single-controller guard, **not distributed fencing or
automatic failover**. Processes that predate this guard do not participate;
stop them before upgrading. Session run receipts are durable; replay remains local.

1. Build and publish the helper image from `ci/Dockerfile.sandbox-helper`:

   ```sh
   docker buildx build --platform linux/amd64,linux/arm64 \
     -f ci/Dockerfile.sandbox-helper \
     -t registry.example.com/at-sandbox-helper:<version> --push .
   ```

2. Review and adapt `deploy/kubernetes/sandbox-rbac.yaml`. AT runs in `at` with
   `serviceAccountName: at-sandbox-controller`; user pods run in `at-sandboxes`.
   Use one replica and a **Recreate** deployment strategy to avoid overlapping
   owners during upgrades. Never start a second controller for the same
   `deployment_id`. The RBAC role can manage all pods/PVCs in the sandbox
   namespace: keep that namespace dedicated, including its admission policies.
3. Verify the CNI enforces ingress/egress policies, including IPv6, DNS and
   metadata denial. Add public cluster/node/control-plane CIDRs to
   `blocked_cidrs`; the default exclusions cover private, loopback, link-local
   and multicast ranges, not every possible cluster topology. Node traffic and
   host-network DNS behavior are CNI-dependent. Do not add a broad allow policy
   to this namespace: Kubernetes policies combine their allowed traffic.
4. Configure `podPidsLimit` on every eligible node and specify its actual value.
   AT refuses a sandbox requesting a smaller ceiling. This is an operator
   assertion, not an API-discovered kubelet guarantee. A RuntimeClass/dedicated
   node pool and CSI-enforced quotas are recommended for untrusted workloads.
5. Configure bootstrap YAML (replace image, classes and limits for your cluster):

   ```yaml
   server:
     sandbox:
       backend: kubernetes
       kubernetes:
         namespace: at-sandboxes
         deployment_id: at-main
         helper_image: registry.example.com/at-sandbox-helper:<version>
         storage_class: workspace-storage
         workspace_size: 20Gi
         home_storage_class: shared-home-storage
         home_access_mode: ReadWriteMany
         home_size: 5Gi
         pod_pids_limit: 256
         network_policy_enforced: true
         single_replica: true
         # runtime_class: gvisor
         # blocked_cidrs: ["203.0.113.0/24"]
         # kubeconfig: /operator-owned/config  # local development only
   ```

Credentials default to in-cluster service-account auth. The helper image must be
pullable by the sandbox namespace; configure its default service account's image
pull secrets if necessary. AT never mounts that service account's API token in
the sandbox. User images must be Linux and have a shell for terminals; file
operations require neither shell nor Python/tar. Existing quota checks still use
`du`, and source control needs `git`, so choose an appropriately prepared image.

Pod provisioning is bounded at three minutes. Inspect Pending/ImagePull errors
with the operator's Kubernetes tools; fix storage, image or scheduling admission
before retrying. Stop removes the pod, preserving PVCs and its network policy.
Reset removes the workspace PVC and policy, but not the account home. Home reset
removes every managed pod mounting that home, then the home PVC. PVC deletion
may remain pending behind storage finalizers; AT does not remove finalizers.
Actual volume erasure depends on the StorageClass/PV reclaim policy: a Retain
policy keeps storage after PVC deletion and requires separate operator cleanup.

### Backend migration

There is no automatic Docker-to-PVC migration. Stop all runs/terminals and AT,
back up each account/workspace volume and account home, configure Kubernetes,
then import each backup into its correctly scoped PVC using operator tooling.
Keep the Docker backup until restore checks pass. Do not run both controllers
against the same accounts during migration. Switching configuration back to
Docker does not delete PVCs, and switching to Kubernetes does not delete Docker
volumes. Keep prepared image versions and helper digests recorded with backups.

### Required cluster acceptance checks

- File read/write/upload and terminal resize/Ctrl-C with a Python-less image.
- Exit status, stdin and cancellation of a command with child/background jobs.
- Stop/start and node replacement preserve workspace/home files; system packages
  disappear as disclosed. AT restart finds the same managed pod/PVCs.
- Foreign labels/UIDs are refused; deletion failures can be retried.
- RWO homes reject a second live space; RWX homes work across two nodes.
- Disabled networking denies DNS/public/private traffic; enabled networking
  permits intended DNS/public traffic and denies metadata/control-plane targets.
- CPU, memory, ephemeral storage, kubelet PID ceiling and CSI disk behavior match
  the configured guarantees. Confirm namespace aggregate quotas too.

Do not infer these guarantees from the fake-client test suite.

### Repeatable real-cluster smoke tests

With Docker, kind, kubectl and Go installed:

```sh
bash ci/test-sandbox-kubernetes.sh
```

The script builds the helper, creates a uniquely named disposable kind cluster
with its own temporary kubeconfig, loads images and runs the opt-in integration
suite. It does not use or alter the caller's current Kubernetes context. It
deletes its cluster and temporary kubeconfig on completion, including test
failure, and prints Pod/PVC/events diagnostics before cleanup on failure.
`AT_TEST_KIND_NODE_IMAGE` overrides the pinned Kubernetes 1.35 image and
`AT_TEST_KUBERNETES_IMAGE` selects the sandbox image. A sandbox image used by this
suite needs `sh`, `cat`, `stat`, `stty`, `sleep`, `grep` and `touch`; the helper
itself requires none of those to copy its static files.

To run against a separately prepared **test cluster**, explicitly set
`AT_TEST_KUBERNETES_KUBECONFIG`, `AT_TEST_KUBERNETES_HELPER_IMAGE` and
`AT_TEST_KUBERNETES_IMAGE`, then run:

```sh
go test -race -count=1 -timeout=8m -v ./internal/service/sandboxkube \
  -run '^TestKubernetesCluster'
```

Tests create random, baseline-policy namespaces and remove only namespaces they
created. They need permission to create/delete namespaces and sandbox resources,
a default dynamic storage class supporting RWO and pullable/loaded images.
The fixture administrator also needs permission to create ServiceAccounts,
Roles/RoleBindings and impersonate the test service account. Actual sandbox
operations use the **Role loaded from `deploy/kubernetes/sandbox-rbac.yaml`**,
not the administrator. Negative checks refuse other namespaces, Secrets and
unrelated controller Leases. This verifies the shipped controller RBAC, not CNI
isolation or the security of a production administrator's additional bindings.
They deliberately use a test-only Driver construction rather than falsely
asserting that kindnet enforces production NetworkPolicy. Normal `go test`
skips them unless the explicit kubeconfig variable is present.

**This is not a production isolation certification:** kind's default kindnet
does not enforce NetworkPolicy, local-path PVCs prove same-node persistence only,
and administrator credentials are used only for fixtures; sandbox operations
use the shipped namespace Role. Production CNI denial, CSI/RWX across nodes,
PID limits, arm64 and the production account's effective RBAC still require
additional acceptance tests. A successful kind run does not enable
multi-replica mode.

## Target architecture

- One sandbox per existing account/workspace scope, with deterministic resource
  names and explicit AT-managed labels.
- A dedicated sandbox namespace, separate from the AT application namespace.
- One workspace PVC per scope; pod replacement never deletes it.
- An optional account-wide home PVC. Home reset removes all pods mounting it,
  then that PVC, without removing workspace PVCs.
- Kubernetes API clients using in-cluster authentication, or an operator-owned
  kubeconfig for local development. No browser-supplied kubeconfig, namespace,
  service account, volume name, host mount or pod template.
- PostgreSQL-backed runtime ownership and run coordination shared by replicas.
- Pod exec streams for commands and TTY terminals; no per-sandbox Service,
  public port or ingress required.

The new backend implements `container.Driver`, `FileInstaller` and
`HomeRemover`. Keep admission and rooted filesystem checks in the existing
handlers and `internal/devfs`; a different backend grants no extra authority.

## Runtime contract and installed packages

Kubernetes has no equivalent of retaining a stopped Docker container's writable
layer. A deleted or restarted pod loses changes outside mounted volumes.

The backend reports explicit runtime capabilities covering:

- whether stopping preserves the root filesystem;
- whether persistent homes and direct file installation are supported;
- whether the runtime is distributed;
- the scope and strength of PID/disk isolation.

Docker keeps its current contract. Kubernetes Stop deletes the pod and retains
its PVCs; the UI must explain that installed packages disappear. Do not silently
claim the current `Driver.Stop` root-filesystem preservation guarantee.

For reproducible Kubernetes spaces, use operator-approved development images
with tools preinstalled. Pin production images by digest. Repository files and
home settings persist independently. A terminal may install packages for the
current pod, but that is not a durable environment definition. Do not attempt
to persist `/`, `/usr` or `/etc` with a generic PVC or commit arbitrary user
containers to a registry.

Existing Docker volumes are not Kubernetes PVCs. Switching backends requires an
explicit backup/import procedure while execution is stopped; never move or
delete storage automatically on a backend config change.

## Storage and resource limits

- Workspace and home storage classes and sizes are bootstrap settings owned by
  the operator. Revalidate requested workspace size before creating resources.
- Account-wide homes need RWX storage for simultaneous spaces on different
  nodes. If only RWO is configured, refuse unsupported simultaneous use with an
  actionable error; do not leave pods indefinitely Pending.
- Define PVC expansion support and minimum sizes. A smaller UI quota does not
  shrink an existing PVC.
- PVC requested capacity is not universally a hard filesystem quota. Document
  the CSI/filesystem guarantees and test the selected storage class. The current
  `du` checks are best-effort admission checks, not hard isolation, and terminals
  can write between checks.
- CPU/memory requests and limits, bounded ephemeral storage, namespace quotas
  and LimitRange are required. Normalize Docker-style memory values (`4g`) into
  validated Kubernetes quantities rather than forwarding them verbatim.
- Kubernetes has no portable per-pod PID limit field. Require the operator's
  kubelet `podPidsLimit` and a compatible runtime; do not ignore `PidsLimit` while
  claiming it is enforced. An operator runtime profile must state the actual
  bound, and incompatible requested limits must fail explicitly.

## Security baseline

- Disable service-account token automount in sandbox pods.
- No privileged mode, host network/PID/IPC, hostPath or host sockets.
- Drop all capabilities, disable privilege escalation, use RuntimeDefault
  seccomp. Root and the existing package-manager capability subset require a
  clearly documented namespace policy; they are not compatible with every
  Restricted Pod Security profile.
- Prefer non-root prepared images. If untrusted multi-tenant code is supported,
  offer an operator-selected gVisor/Kata RuntimeClass; ordinary pods alone are
  not a strong boundary against kernel exploits.
- Apply ingress default-deny and explicit egress policy before running code.
  `Network: false` means deny all pod traffic. Enabled networking must not mean
  access to the cluster control plane, metadata endpoints or internal services.
  DNS, approved mirrors and public-network policy need CNI-specific verification,
  including IPv6. Merely creating NetworkPolicy objects is not proof of isolation.
- Namespace-scoped RBAC for pods, pod exec, PVCs and policies; add other resources
  only when needed. Do not grant cluster-admin. Image pull secrets and runtime
  profiles are installation-owned.
- Validate resource labels, scope and object UID before deleting or attaching;
  matching a resource name alone is not ownership proof. Never adopt foreign
  resources or remove finalizers to force cleanup.

## Commands, terminals and file delivery

Use Kubernetes exec streaming with context cancellation, separate stdout/stderr,
stdin, exit-code propagation and a terminal resize queue. Closing an attachment
ends only that shell. Test both supported stream transports against the actual
cluster; do not fall back to subprocess shells that hide cancellation.

Kubernetes exec does not provide Docker's independent working-directory/env
fields. Define a validated launcher that preserves argv exactly, sets env and
changes directory without interpolating user strings into shell commands.

Install `at-devfs` through a versioned, multi-architecture helper image/init
container and an isolated helper volume, rather than depending on `kubectl cp`,
tar or python in the user's image. Add a bounded stdin-based file-install
operation to the helper for home uploads, with containment and symlink checks.
Keep helper installation outside user storage and include its version in the
runtime configuration hash.

## Replica coordination and recovery

Deterministic pod names prevent duplicate names, but do not prevent a replica
from stopping another replica's active environment. Sticky routing alone does
not solve this.

Introduce durable scope state with desired configuration hash, pod UID,
generation/fencing version, lifecycle status, owning replica lease and expiry.
Use atomic compare-and-set transitions; do not hold a SQL row lock across a
slow Kubernetes API call. Lifecycle work must verify its fencing generation
before and after external operations. Reconciliation repairs partial creation,
API timeouts and process crashes by reading actual managed objects.

Active commands, terminals and agent runs renew durable activity leases. Idle
cleanup runs only when no live activity lease exists. An owner losing its lease
must stop dispatching commands and cancel its local runs. Explicit Stop/Reset
records a desired transition and reaches all affected owners; it cannot cancel
only the replica handling the HTTP request. Shutdown relinquishes ownership,
not other replicas' sandboxes.

Developer sessions now have durable single-run exclusion (migration 100). SSE buffers currently
live in one replica: either route replay to the owning replica and return an
explicit unavailable result after restart, or persist bounded replay events.
Never retry POST automatically on a lost replay stream. Pending approvals and
completed messages remain in PostgreSQL. Failure recovery must mark interrupted
runs terminal rather than leave sessions busy forever. The current implementation
reports expired owners but does not steal their receipts or restart their work.

### Durable Developer session run receipts

Every run, confirmation and answer acquires an account/workspace-scoped receipt
on `developer_sessions` before executing its handler. Its ID is server-generated
and independent of the client-selected SSE replay ID. Atomic acquisition excludes
another process from the same session. The owner renews every 15 seconds, with a
five-second database-call deadline and a 60-second stale threshold measured by
PostgreSQL. Cancellation on any replica sets the persisted cancellation flag;
the owner observes it on renewal or before its next tool dispatch. Database errors
also cancel local execution. Session history, pending tools and snapshots lock
the session row while checking ownership and writing; stale/cancelled runtime
writes cannot overwrite the recorded outcome.

Active-stream discovery returns a `run` receipt separately from `stream_id`.
A missing local SSE stream never means the job may be restarted. Discovery marks
a running session with an expired receipt as failed with an interruption message;
it keeps the receipt. The UI disables sending/approval/settings while any receipt
is unresolved and explains cancellation and recovery. Normal handler exit releases
its own receipt; stored waiting approvals survive a normal release.

**This is not remote fencing or automatic failover.** An already-dispatched tool
may have taken effect, and cancellation of remote work can fail during outages.
A dead owner cannot release its receipt. Stop records a cancellation request, not
proof that the old process is gone; it never clears an expired receipt. Before
using a new session, stop/verify the former process and any remote work, recover
the controller Lease as described above, and inspect saved history/files. There
is intentionally no automatic tool replay or API to force-clear the receipt.
Existing pre-migration running rows have no receipt; drain/stop older AT processes
before upgrading. Durable replay, lifecycle/activity fencing, safe receipt repair
and cross-replica activity/account cleanup remain separate work.

### Durable Start/Stop/Reset control receipts

Migration 101 adds `execution_suspended` and `active_control_id` to Developer
spaces. Start, Stop and Reset acquire a server-generated control receipt under
the parent row lock; run acquisition uses the same lock. Stop/Reset atomically
suspend new runtime admission and request cancellation of every active session.
Only an explicit successful Start reopens admission, and Start is refused while
any run receipt remains unresolved. Control receipts never expire or transfer
automatically after a crash. A database failure leaves admission closed.

Session deletion and space deletion/reconfiguration cannot erase live or expired
run receipts. Reset requests cancellation first and returns 409 while a receipt
remains; retry after owners finish. It drains local commands/terminals and purges
the runtime before deleting records, retaining the space on cleanup failure.
Start/Stop resolve an untracked workload by stable scope, so a fresh Manager does
not confuse an empty local handle cache with proof that the sandbox is stopped.
The Kubernetes backend still requires its controller Lease for that operation.

Manager activity registration is atomic with local provisioning. Stop/Reset close
local admission, cancel active operations and wait for their backend calls or
terminal Close to return before removing the workload/storage. A drain timeout
or removal failure retains tracking and blocks new work until explicit cleanup
is retried. Reconfiguration and idle cleanup refuse active work.

**These controls are not distributed activity leases or remote fencing.** A
runtime request admitted before suspension can still race control on another
process. Terminal/file activity and idle timers remain process-local; account
deletion and home reset do not yet have distributed lifecycle coordination.
Do not enable multiple Kubernetes replicas based on these receipts. Recovery of
an orphan control receipt requires stopping its former owner, verifying remote
termination and inspecting runtime/storage state; there is no force-clear API
or automatic recovery. The new store/fake-backend tests do not replace a new
real-cluster CNI/CSI or multi-node validation.

## Implementation milestones and acceptance

1. **Lifecycle foundation:** retain tracking and surface driver failures.
   Implemented with fake-driver regression tests, typed runtime capabilities
   and backend-aware Stop semantics.
2. **Driver/bootstrap:** client-go backend, operator config validation, managed
   Pod/PVC creation/reconciliation, UID-safe deletion and resource limits.
   Implemented; fake API regressions and single-node kind lifecycle tests pass.
   Reusing a Pod reconciles its PVC/policy dependencies; removal respects
   termination grace and waits instead of force-deleting the API object.
3. **Exec/files/terminal:** launcher, helper image, stream cancellation, resize,
   Implemented with local launcher/PTY and real kind exec/TTY tests. Production
   cluster compatibility and arm64 image verification remain. Terminal Close
   waits for remote cancellation before releasing local activity tracking.
4. **Storage/security:** home access modes, CSI expansion behavior, policy and
   RBAC manifests, network-denial tests, PID/runtime admission. Admission and
   example manifests exist; actual CNI/CSI/PID guarantees remain unverified.
5. **Distributed lifecycle:** database migration, fenced ownership/activity
    leases, reconciler, cross-replica Stop/Reset/account cleanup and crash recovery.
    Durable Start/Stop/Reset exclusion and suspension, cancellation propagation,
    receipt-preserving deletion and local draining are implemented. Distributed
    runtime activity, remote fencing, reconciliation and account cleanup remain.
6. **Runs/replay:** durable run ownership, reconnect routing or persisted replay,
    cancellation propagation and owner-death handling.
    Durable exclusion receipts, cancellation flags and interruption reporting are
    implemented with PostgreSQL/concurrency tests. Safe automatic repair, durable
    replay and remote-effect fencing are not implemented.
7. **Product/deployment:** runtime-aware UI warnings, prepared image examples,
   backup/import instructions, Kubernetes manifests and operational runbook.

Production acceptance requires two AT replicas and actual Kubernetes + CSI/CNI:
concurrent first use creates one environment; one replica's idle cleanup cannot
stop another's terminal; restart and node replacement retain PVC data; home reset
affects only its account; lost API/DB/owner connections fail safely; helper and
terminal work without image Python/tar; denied networks/metadata stay denied;
quotas and PID guarantees match the declared runtime capabilities; pod/PVC
cleanup is retryable without claiming failed deletions succeeded.
