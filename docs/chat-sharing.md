# Chat sharing

Chats can publish a completed conversation prefix to a workspace. A share is an
explicit, versioned snapshot—not a live view. Messages appended to the source
remain private until the owner chooses **Update snapshot**, and revoking a share
immediately prevents future reads and imports.

## Ownership and access

- The source conversation remains private to its account owner.
- A share belongs to one workspace. Reading and importing revalidate current
  workspace membership and the `models.use` capability on every request.
- Only the source owner can publish, update, or revoke its share.
- An imported conversation is a new account-owned Chat. It survives share or
  source deletion and records the share ID/version it came from.
- The `chat_workbench`, `playground`, and `chat_sharing` feature switches all
  have to admit the operation. The `gateway_chat`, `agent_platform`, and `full`
  presets enable sharing.

## Portable snapshot boundary

The owner chooses a completed assistant response as the boundary. AT copies the
prefix through that message, validates tool-call/result pairing, and refuses
oversized or incomplete prefixes rather than silently truncating them. Snapshot
payloads are capped at 2,000 messages and 8 MiB.

System prompts, historical tool calls/results, and image attachments are each
excluded by default and require an explicit checkbox. Conversation workbench
configuration, agents, skills, MCP endpoints/headers, credentials, usage data,
and active execution state are never copied. Included tool records are history;
importing them does not execute a tool.

Included images are physically copied into share-owned media objects. Importing
creates a second recipient-owned copy, so neither the shared view nor the new
Chat relies on the source owner's private media URLs. Revocation and workspace
deletion remove share-owned records and best-effort delete their backing blobs.

## Providers and continuation

A share transfers no provider credential or authorization. The read-only page
offers only models visible to the recipient in the selected workspace. When the
author's provider is private or otherwise unavailable, the recipient must choose
an accessible model there or after opening the copied Chat. Imported workbench
configuration starts empty, so disabled tools or unavailable dependencies cannot
be inherited. Subsequent model calls use the recipient's browser identity,
workspace policy, provider access, usage attribution, and trace attribution.

## HTTP API

All routes are under `/api/v1`, require native authentication, and use
`X-AT-Workspace-ID` (or the scoped query selector for share media):

- `POST /chats/conversations/{id}/shares/preview`
- `POST /chats/conversations/{id}/shares`
- `GET /chats/conversations/{id}/share`
- `GET|PUT|DELETE /chats/shares/{id}`
- `POST /chats/shares/{id}/import`
- `GET /chats/shares/{id}/media/{media}`

Publish/update bodies contain `through_sequence` and `options`. Import requires
the exact current `version`; a concurrent update or revoke returns a conflict.

## Upgrade behavior

Migration `68_chat_shares.sql` adds the share/version/media tables and optional
import provenance columns to existing Playground conversations. Existing Chats,
messages, providers, and media objects are unchanged. Fresh feature catalogs
enable sharing by default; installations applying an explicit feature preset get
the preset behavior described above.
