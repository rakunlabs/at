# YouTube Documentary Publishing

The `youtube-publish` template uploads **private drafts only** through the YouTube Data API v3. For English documentaries, the intended workflow is a landscape master, English metadata, a private upload, and user review. Public publication is a manual action in YouTube Studio, never an automatic follow-up task.

This guide documents the existing handlers, including their limitations. It does not add runtime uploads, repair OAuth, update the Shorts detector, or implement resumable recovery. Finish and validate the code before deployment; configure runtime MCP agents separately afterward. The examples below are configuration references, not instructions to run a live upload during development.

## Connect the Intended Channel

1. After deployment, install the **YouTube Publisher** template (`youtube-publish`, installed skill name `youtube_publish`) from the Skill Store.
2. Create a named **YouTube Connection** in Connections and authorize the intended Google/YouTube channel. The handler requires `youtube_client_id`, `youtube_client_secret`, and `youtube_refresh_token`; store these in the Connection, not in task briefs or prompts.
3. Bind the Connection ID under provider key `youtube` on the uploader agent, or override it for this skill. Confirm the actual channel in YouTube Studio rather than relying only on the Connection's display name.
4. Configure the uploader's outer `tool_timeout` in seconds for the expected staging and upload duration, and check surrounding task/client deadlines. A short outer timeout cancels the handler even if curl allows longer.

The updated `agent_create` and `agent_update` management tools accept an agent-level `connections` map and structured `skills` entries. These fields require the updated deployment and refreshed MCP tool discovery; older running tool schemas may still expose only string skill references.

Example `agent_update` binding fields (replace placeholders with actual IDs later):

```json
{
  "id": "<uploader-agent-id>",
  "connections": { "youtube": "<youtube-connection-id>" },
  "skills": [{ "id": "youtube_publish" }]
}
```

For a per-skill override, use this structured entry in the agent's `skills` array:

```json
{
  "id": "youtube_publish",
  "connections": { "youtube": "<youtube-connection-id>" }
}
```

`agent_create` accepts the same binding fields alongside its agent creation fields. Resolution priority is per-skill override, then agent binding, then global variable fallback. Ensure the named Connection is complete so missing credentials do not fall back to an unintended global account. On update, each supplied `skills` array or `connections` map replaces that whole field: fetch the agent first and preserve unrelated entries. Omitted fields remain unchanged.

## Prepare an English Documentary

Use a landscape master, typically 16:9. For an authorized upload, call `refresh_youtube_token` immediately beforehand and pass its access token to `upload_to_youtube`. Keep the token out of logs, documentation, and user-facing results.

Illustrative upload arguments, not a live request:

```json
{
  "access_token": "<fresh-access-token>",
  "video_url": "/srv/documentaries/episode-01.mp4",
  "title": "How the City Rebuilt Its Waterways | Documentary",
  "description": "An English documentary about the city's waterways.\n\nSources: <actual source titles, authors, dates, and URLs used in this episode>.\n\nReconstruction disclosure: <identify scenes or audio that are AI-generated or dramatized; distinguish them from verified archival material>.\n\nCredits and rights: <actual footage, music, and image credits and licenses>.",
  "tags": ["documentary", "history", "education"],
  "category_id": "27",
  "is_short": false
}
```

Replace all editorial placeholders before upload. Use an English title of at most 100 characters and an English description with a synopsis, real sources, credits, and an accurate reconstruction disclosure. Do not invent references or describe generated reconstructions as archival evidence. Category `27` is Education; it does not determine Shorts eligibility. The tool does not set video language or YouTube's altered/synthetic-content disclosure fields. Review those settings manually in Studio; prose disclosure does not replace platform requirements.

## Shorts Are Determined by YouTube

`is_short` is a local metadata/URL switch, not an API command that sets YouTube's classification:

| Value | Current handler behavior |
| --- | --- |
| `true` | Adds `#Shorts` if missing and returns a `/shorts/<id>` URL. |
| `false` | Does not add `#Shorts` and returns `/watch?v=<id>`. Does not strip an existing hashtag. |
| Omitted | Uses the legacy ffprobe test: positive dimensions, `height >= width`, and `0 < duration <= 60.5` seconds. Missing ffprobe or invalid probe values default to `false`. |

YouTube's current rules categorize square or vertical uploads up to **3 minutes** as Shorts for videos uploaded on or after October 15, 2024, subject to YouTube's applicable rules. The handler's **60.5-second** detector has not been fixed to match this. Neither omitting `#Shorts`, passing `is_short:false`, nor returning a watch URL prevents YouTube from categorizing eligible footage as a Short. For landscape documentaries, pass `is_short:false` explicitly rather than relying on this detector.

## Upload Limits

| Boundary | Limit and caveat |
| --- | --- |
| YouTube channel eligibility | By default, uploads are limited to 15 minutes. Verify the intended account/channel is enabled for longer uploads before sending a documentary over 15 minutes. |
| YouTube maximum | **256 GB or 12 hours, whichever is less**. Account restrictions, quota, copyright checks, and processing failures can still block an otherwise in-range upload. |
| AT browser files API | `POST /api/v1/files/upload` caps the **entire multipart request** at `256 << 20` = **268,435,456 bytes (256 MiB)**. Multipart overhead makes the usable file size slightly smaller. A reverse proxy may impose a lower limit. |
| Server-local source | Passing a path accessible to the AT handler does not go through the browser files API and has **no same 256 MiB cap**. It remains subject to YouTube limits, disk capacity, permissions, and deadlines. A path on the user's laptop is not automatically server-local. |
| HTTPS source | The handler downloads the source before uploading. URL availability, download time, and temporary disk capacity matter. |

The handler copies a local source into its temporary workspace, or downloads a URL there. Budget disk space for that additional full file; server-local upload is not zero-copy or unlimited.

## Timeouts and Unknown Outcomes

Current curl settings are request-level limits, not an end-to-end upload guarantee:

| Stage | Current settings |
| --- | --- |
| HTTPS source download | 30-second connect timeout, `--max-time 1800` (30 minutes), `--retry 3`, 5-second retry delay. |
| Resumable session initiation | 30-second connect timeout, `--max-time 60`, `--retry 3`, 5-second retry delay. |
| Video PUT | 60-second connect timeout, `--max-time 14400` (4 hours), **no curl retry option**. |

Curl retries apply only to eligible failures; they do not guarantee success, and retry time can extend a stage beyond a single attempt's timeout. The outer agent `tool_timeout`, enclosing task deadline, cancellation, or shutdown can stop the whole handler earlier. Allow for staging, initiation, upload, and retries when selecting the outer timeout later. The four-hour PUT setting is not a promise that the entire operation gets four hours or finishes within four hours.

Although the handler uses YouTube's resumable session endpoint, it does not persist the session for recovery, query the received byte offset, or resume an interrupted transfer. It offers no guaranteed recovery from premature EOF, timeout, token expiration, or account-limit errors.

**If the result is missing, times out, or is otherwise unknown, do not immediately upload again.** First have the user check the intended channel's Content list in YouTube Studio, including private and processing videos. A video may have been accepted even if AT never received its ID. Reconcile any existing draft before deciding whether another upload is needed, to avoid duplicates.

## Review Before Publication

After a successful response, record the returned video ID and private URL. Upload acceptance is not proof that YouTube processing has finished or the documentary is ready for release.

Ask the user to review the private draft in YouTube Studio: playback and audio, English title/description and language settings, sources and credits, reconstruction and altered-content disclosures, rights checks, thumbnail, audience settings, and privacy. The current handler sends `selfDeclaredMadeForKids:false`; review whether that is appropriate. Keep the video private until the user chooses to publish manually. Do not schedule or perform automatic public publication.

## References

- [YouTube: Upload videos longer than 15 minutes and maximum upload size](https://support.google.com/youtube/answer/71673)
- [YouTube: Understand three-minute Shorts](https://support.google.com/youtube/answer/15424877)
- [YouTube Data API: Resumable upload protocol](https://developers.google.com/youtube/v3/guides/using_resumable_upload_protocol)
- [YouTube: Disclosing altered or synthetic content](https://support.google.com/youtube/answer/14328491)

Consult the current YouTube requirements before a real upload. These references are not evidence of a live account check or upload test.
