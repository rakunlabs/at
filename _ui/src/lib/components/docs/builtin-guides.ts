import type { DocsSearchable } from '@/lib/helper/docs-nav';

/** A guide as the UI renders it — built-in (read-only) or user-authored. */
export interface DisplayGuide extends DocsSearchable {
  id: string;
  title: string;
  description: string;
  iconName: string;
  content: string;
  builtin: boolean;
  /** Search index — mirrors `content`; see DocsSearchable. */
  body: string;
}

function builtin(
  id: string,
  title: string,
  description: string,
  iconName: string,
  content: string,
): DisplayGuide {
  return { id, title, description, iconName, content, builtin: true, body: content };
}

/** Built-in guides ship with the app and cannot be edited or deleted. */
export const builtinGuides: DisplayGuide[] = [
  builtin('free-tiers', 'Personal free-tier setup', 'Connect your own accounts without pooling other users’ credentials', 'KeyRound', `
## Connect your own free-tier accounts

Open **Providers → Free-tier setup and workspace policy**. AT supplies setup documentation, not tokens or an entitlement to a provider's free plan.

1. Select official API providers and create your own accounts using the registration links.
2. Paste each key, choose the models eligible under your plan and copy limits from your account console. Model hints are suggestions, not a promise of eligibility.
3. Set optional daily/monthly request and token ceilings. A provider-wide pool is shared across models; model-specific limits are additional ceilings. Never count the same upstream pool once per model.
4. Confirm data-use and billing terms. Some free plans permit training on submitted content or are evaluation-only. No shared-use or resale permission is implied.
5. Create the providers and a personal priority profile. An optional **existing paid fallback** is always last and may incur charges. With no fallback, exhaustion returns an error rather than deliberately choosing a paid model.
6. If permitted, create a model-restricted personal gateway token (30-day expiry). Copy it once. Shared workspace tokens cannot reach your private profile.

### How quota enforcement works

Counters and reservations are stored in PostgreSQL by physical provider ID, shared across users and replicas. Windows reset at UTC midnight and the first of each month. AT reserves estimated input plus the requested output allowance (32,000 output tokens when omitted), then settles reported usage. No output cap is injected. Use an explicit output allowance for small token quotas. Missing usage retains the estimate; failed upstream attempts still count as requests. A crash leaves its reservation charged conservatively until rollover. Opaque calls cannot use token quotas without an estimate: use request ceilings for those calls.

Quota exhaustion returns 429 with code provider_quota_exceeded and Retry-After, and advances a configured fallback chain without another upstream call. The local ceilings do not include activity outside AT, do not reconcile multiple AT provider rows using the same upstream account, and do not guarantee zero charges. Disable upstream automatic top-up or paid usage where the provider supports it.

### Workspace control

Workspace administrators may block catalog free-tier personal providers and known catalog endpoint hosts. This conservatively blocks paid accounts on those hosts too. An optional exact-host HTTPS allowlist covers all personal endpoints (an empty list permits none); proxies and TLS opt-out are refused with that list. Policy is checked on create/update, catalog reads and live server-side provider resolution. Other workspaces keep their own policy. These are provider controls, not a network sandbox: trusted-host tools, browser-local providers and external relays require separate controls.

### Partial setup and maintenance

Setup is not one database transaction: successful providers are retained if a later step fails. The page names saved references and never retries writes automatically. **Continue setup** checks stable provider keys and reuses completed steps. You can correct a profile name conflict without creating duplicate providers. If a token response was lost, use API Tokens to rotate that token instead of creating another blindly. Edit a provider's daily/monthly quota section to see usage and configure additional model pools.

Personal profiles are workspace-bound and private to their account. They appear in that account's personal-token gateway model catalog; their names are unique in the workspace. The ordinary Routing profiles page shows your profiles when you have its management permission. No one else's key is borrowed on exhaustion.

### Existing aggregators

A self-hosted FreeLLMAPI or OmniRoute can be added as a custom OpenAI-compatible personal provider, using its own base URL and unified key. That is optional, not required by this setup. AT sees aggregate usage for that endpoint, not its underlying account balances. In Chats a browser-local provider can instead reach an aggregator on your computer; server-side workspace quotas do not apply to browser-local calls.
`),
  builtin(
    'local-providers',
    'Local providers',
    'Use your own OpenAI-compatible model server or API key from Chats',
    'Cpu',
    `
## Local providers

A **local provider** is an OpenAI-compatible endpoint that **your browser calls directly** from Chats. It can be a model server on your own computer (Ollama, LM Studio, llama.cpp, vLLM) or a hosted API with your own key.

AT stores the provider's address and credentials and **never sends a request to it**. That is what makes \`localhost\` mean *your* computer rather than the AT server.

Only OpenAI-compatible APIs are supported: the browser calls \`<base URL>/models\` and \`<base URL>/chat/completions\`.

### Adding one

1. Open **Chats → Workbench → Local providers** and choose **Add provider**.
2. Enter a **name** (lowercase, used in the model reference \`local:<name>/<model>\`) and the **base URL**, for example \`http://127.0.0.1:11434/v1\`.
3. Optionally enter an **API key** (sent as \`Authorization: Bearer …\`) and extra headers. They are stored encrypted and fetched only when your browser is about to call the provider.
4. Choose **Enable here**. The browser lists the models and adds them to the model picker under *<name> · This device*.

Plain \`http\` is accepted only for local addresses (loopback, private networks, \`.local\`). Public endpoints must use \`https\`.

### Enabling is per device

The provider record follows your account, but **Enable here** is stored in this browser only. \`http://127.0.0.1:11434\` is a different program on your laptop and on your desktop, so a provider must be enabled again on each device before your conversation is sent to it.

### CORS: the provider must allow this page

Because the request comes from the browser, the provider must answer cross-origin requests from AT's address:

- \`Access-Control-Allow-Origin\` for AT's origin
- \`Access-Control-Allow-Headers\` including \`authorization\` and \`content-type\`
- When AT is on a public address and the provider is on a local one, current Chrome also requires \`Access-Control-Allow-Private-Network: true\` on the preflight.

Examples:

\`\`\`bash
# Ollama
OLLAMA_ORIGINS="https://at.example.com" ollama serve

# LM Studio: Developer → Server settings → Enable CORS
\`\`\`

A browser reports "server not running", "CORS refused" and "certificate not trusted" the same way (*Failed to fetch*), so the error message names every cause.

### Another AT server as a local provider

AT's gateway already allows browser calls from any page, so another AT installation needs no configuration:

- **Base URL:** \`https://<other-at>/gateway/v1\` (include its sub-path, e.g. \`https://host/at/gateway/v1\`)
- **API key:** an API token created on that server (\`at_…\`)

Its models and routing profiles appear in the picker. Budgets, token limits and traces apply on that server.

### Self-signed or untrusted certificates

A browser cannot be told to skip certificate verification, so there is no "insecure" option for local providers. Either:

- trust the certificate on this device (for example \`mkcert -install\`, or import the CA into the operating system's trust store), or
- add the endpoint on the **Providers** page as an \`openai\` provider with **Insecure skip verify** enabled. The AT server then makes the call, and server-side budgets, fallbacks and usage apply.

Hosted APIs differ: OpenAI-compatible services that allow browser requests work directly; ones that refuse cross-origin requests cannot be used this way. Add those as a normal (server-side) provider on the **Providers** page instead. Anthropic's native API is not supported here.

### What does not apply

The call never passes through the AT server, so:

- **Provider budgets, per-user allowances, API-token limits and pricing do not apply.** You pay the provider directly with your own key.
- **The loop governor does not apply** (no input windowing or tool-result spill files); the context window is the model's own.
- **Usage and Cost** are not updated. Each call is recorded in **Traces** as a generation marked \`origin: browser_local\` and \`client_asserted: true\`, with the token counts the provider reported and no cost. Prompt content is included only while LLM body capture is on.
- A workspace administrator can turn the whole feature off under **Settings → Features → Local providers in Chats**.

### Conversation history

Saved messages are normally sent to the server by reference. A local provider needs the whole conversation in the request, so the browser sends every message inline, including attachments loaded from media storage. If a long conversation has older messages that are not loaded yet, choose **Load older messages** first; AT refuses to send a shortened context silently.

### Other limitations

- **Chats only.** Sessions, organization tasks, workflows and bots run on the server and cannot reach a provider that only your browser can.
- **No reconnect.** Server completions can resume after a dropped connection; a local provider call cannot. If the connection drops, retry the turn.
- **Tools still work.** Built-in, MCP set, skill, local MCP and extension tools are dispatched exactly as with server providers. Some local servers send tool calls without IDs; the browser assigns them.
- **No reasoning-effort control.** AT does not know a local model's capabilities, so the reasoning picker is hidden and no \`reasoning_effort\` is sent.
`,
  ),
  builtin(
    'whisper',
    'Speech-to-Text (Whisper)',
    'Voice message transcription for Telegram bots and agents',
    'Mic',
    `
## Speech-to-Text with Whisper

AT supports automatic voice message transcription. When a user sends a voice message in Telegram, it's automatically transcribed to text before reaching the agent.

### Option 1: OpenAI Whisper API (Recommended)

**No setup needed** — just set the \`openai_api_key\` variable and voice messages work automatically.

- Uses OpenAI's cloud Whisper API (\`whisper-1\` model)
- Best accuracy, supports 50+ languages
- Cost: ~$0.006/minute of audio
- Max file size: 25MB

**How it works:**
1. User sends voice message in Telegram
2. Bot downloads the audio file
3. Sends to \`/v1/audio/transcriptions\` endpoint
4. Transcribed text is passed to the agent as normal text

### Option 2: Local Whisper (Free, Self-Hosted)

Run OpenAI's open-source Whisper model locally. No API costs, but needs CPU/GPU.

#### Install

\`\`\`bash
# Using pip
pip install openai-whisper

# Using uv (faster)
uv pip install openai-whisper

# Or with conda
conda install -c conda-forge openai-whisper
\`\`\`

**System requirements:**
- Python 3.9+
- FFmpeg (\`brew install ffmpeg\` on macOS)
- ~1GB RAM for \`tiny\` model, ~5GB for \`base\`, ~10GB for \`medium\`
- GPU optional but much faster (CUDA or Apple MPS)

#### Models

| Model | Size | English-only | RAM | Speed |
|-------|------|-------------|-----|-------|
| \`tiny\` | 39M | ✓ | ~1GB | Fastest |
| \`base\` | 74M | ✓ | ~1GB | Fast |
| \`small\` | 244M | ✓ | ~2GB | Good |
| \`medium\` | 769M | ✓ | ~5GB | Better |
| \`large-v3\` | 1.5G | ✗ | ~10GB | Best |

#### Usage from Command Line

\`\`\`bash
# Basic transcription
whisper audio.ogg --model base --output_format txt

# Specific language
whisper audio.ogg --model base --language Turkish

# With GPU (faster)
whisper audio.ogg --model medium --device cuda

# Output as JSON with timestamps
whisper audio.ogg --model base --output_format json
\`\`\`

#### Usage from Python

\`\`\`python
import whisper

model = whisper.load_model("base")
result = model.transcribe("audio.ogg")
print(result["text"])
\`\`\`

#### Document the process as an AT Skill

Create a documentation skill that explains when and how an agent should use an
existing workflow or approved shell tool. Code blocks in skills are examples;
AT never executes them or registers them as tools.

#### Integrate with AT as an Exec Workflow Node

Create an exec node with language=python:

\`\`\`python
import json, os, subprocess

# Install if needed
subprocess.run(['pip', 'install', 'openai-whisper', '--break-system-packages', '-q'],
               capture_output=True)

import whisper

data = json.loads(os.environ.get('AT_NODE_INPUT', '{}'))
audio_path = data.get('audio', '')

model = whisper.load_model('base')
result = model.transcribe(audio_path)

print(json.dumps({
    'text': result['text'],
    'language': result.get('language', 'unknown'),
    'segments': len(result.get('segments', []))
}))
\`\`\`

#### Use with Docker Container

If you have container isolation enabled, add Whisper to your own runtime image's Dockerfile:

\`\`\`dockerfile
# In your runtime image's Dockerfile (requires Python and pip)
RUN pip install --no-cache-dir openai-whisper
\`\`\`

Then agents inside the container can use Whisper without install delays.

### Option 3: Faster-Whisper (Optimized Local)

[faster-whisper](https://github.com/SYSTRAN/faster-whisper) is a reimplementation using CTranslate2 — up to 4x faster than original Whisper.

\`\`\`bash
pip install faster-whisper
\`\`\`

\`\`\`python
from faster_whisper import WhisperModel

model = WhisperModel("base", device="cpu", compute_type="int8")
segments, info = model.transcribe("audio.ogg")

for segment in segments:
    print(f"[{segment.start:.2f}s -> {segment.end:.2f}s] {segment.text}")
\`\`\`

### Comparison

| Feature | OpenAI API | Local Whisper | Faster-Whisper |
|---------|-----------|---------------|----------------|
| Setup | Just API key | Install package | Install package |
| Cost | $0.006/min | Free | Free |
| Speed | ~1s/min | ~10s/min (CPU) | ~3s/min (CPU) |
| Accuracy | Best | Very good | Very good |
| Languages | 50+ | 50+ | 50+ |
| Offline | No | Yes | Yes |
| GPU needed | No | Optional | Optional |
| Max file | 25MB | Unlimited | Unlimited |

### Telegram Bot Configuration

Voice transcription is **automatic** when \`openai_api_key\` is set. No per-bot configuration needed.

To switch to local Whisper, you would need to modify the \`transcribeAudio\` function in the server code to call the local model instead of the API.
`,
  ),
  builtin(
    'containers',
    'Container Isolation',
    'Isolate agent execution with Docker containers',
    'Box',
    `
## Container Isolation

AT supports optional Docker container isolation for agent execution. Each organization or bot user can run in their own isolated container.

### Supply a Runtime Image

AT does not ship an agent runtime Dockerfile. Build or obtain your own image with a shell and the tools your agents need, such as Python, FFmpeg, Node.js, or Playwright, and make it available to Docker on the AT host.

Set the container image field to that image's tag. The default tag, \`at-agent-runtime:latest\`, is only a name; it does not provide an image.

### Per-Organization Containers

All agents in an org share one container:

1. Go to **Organizations** → select your org
2. Click **Container** button in toolbar
3. Enable and configure:
   - **Image**: \`at-agent-runtime:latest\`
   - **CPU**: \`2\` (cores)
   - **Memory**: \`4g\`
   - **Network**: enabled for API calls
4. Save

### Per-User Containers (Bots)

Each Telegram/Discord user gets their own container:

1. Go to **Bots** → edit your bot
2. Enable **Per-user container isolation**
3. Configure image, CPU, memory
4. Save

### What's Isolated

- Filesystem (each container has its own /workspace)
- Python packages
- Temp files
- Running processes
- Network (configurable)

### Lifecycle

- Containers are created on first command execution
- Reused for subsequent commands
- Cleaned up on server shutdown
- Idle containers can be cleaned up automatically
`,
  ),
  builtin(
    'telegram',
    'Telegram Bot Commands',
    'All available commands for Telegram bots',
    'Send',
    `
## Telegram Bot Commands

### Task Management

| Command | Description |
|---------|------------|
| \`/new <topic>\` | Create a background task |
| \`/tasks\` | List recent tasks |
| \`/status [id]\` | Check task status (default: active task) |
| \`/result [id]\` | Get task output + video |
| \`/pick <id>\` | Select task to chat about |
| \`/run <instruction>\` | Run a background subtask on active task |
| \`/current\` | Show active task |

### Session

| Command | Description |
|---------|------------|
| \`/reset\` | Clear conversation history |
| \`/agents\` | List available agents |
| \`/switch <name>\` | Switch to a different agent |
| \`/login [provider]\` | OAuth login (default: google) |
| \`/help\` | Show all commands |

### Workflow

1. \`/new top 5 deadliest animals\` — creates task, runs in background
2. Chat normally while it runs
3. \`/status\` — check if done
4. \`/result\` — get the video
5. \`/pick YTS-5\` — select task to discuss
6. Chat about the task — agent knows the context
7. \`/run upload to youtube\` — run background action on the task

### Voice Messages

Just send a voice message — it's automatically transcribed to text using Whisper. No commands needed.

### File Attachments

Send files (PDF, images, documents) — they're downloaded and the agent can read them. PDFs are extracted to text automatically.

### BotFather Setup

Copy these commands for \`/setcommands\`:

\`\`\`
new - Create a background task
tasks - List recent tasks
status - Check task status
result - Get task output and video
pick - Select task to chat about
run - Run background subtask
current - Show active task
reset - Clear conversation
agents - List available agents
switch - Switch to a different agent
login - Connect your Google account
help - Show available commands
\`\`\`
`,
  ),
];
