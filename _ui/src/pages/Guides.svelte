<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { ChevronDown, ChevronRight, BookOpen } from 'lucide-svelte';
  import { md, renderMarkdown } from '@/lib/helper/markdown';

  storeNavbar.title = 'Guides';

  let openSections = $state<Record<string, boolean>>({ whisper: true });

  function toggle(id: string) {
    openSections[id] = !openSections[id];
  }

  const guides = [
    {
      id: 'whisper',
      title: 'Speech-to-Text (Whisper)',
      description: 'Voice message transcription for Telegram bots and agents',
      content: `
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

#### Integrate with AT as a Skill

Create a custom skill that uses local Whisper:

\`\`\`bash
# In the skill handler:
pip install openai-whisper --break-system-packages -q 2>/dev/null
python3 -c "
import whisper
model = whisper.load_model('base')
result = model.transcribe('/path/to/audio.ogg')
print(result['text'])
"
\`\`\`

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

If you have container isolation enabled, add Whisper to the Dockerfile:

\`\`\`dockerfile
# In Dockerfile.agent-runtime
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
`
    },
    {
      id: 'containers',
      title: 'Container Isolation',
      description: 'Isolate agent execution with Docker containers',
      content: `
## Container Isolation

AT supports optional Docker container isolation for agent execution. Each organization or bot user can run in their own isolated container.

### Build the Runtime Image

\`\`\`bash
docker build -f Dockerfile.agent-runtime -t at-agent-runtime:latest .
\`\`\`

The image includes: Python 3.13, FFmpeg, Node.js, Playwright, common pip packages (pdfminer, Pillow, requests, etc.)

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
`
    },
    {
      id: 'telegram',
      title: 'Telegram Bot Commands',
      description: 'All available commands for Telegram bots',
      content: `
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
`
    },
    {
      id: 'youtube',
      title: 'YouTube Shorts Pipeline',
      description: 'How the video production pipeline works',
      content: `
## YouTube Shorts Pipeline

### Agents

| Agent | Role |
|-------|------|
| **Content Director** | Orchestrates the whole process |
| **Script Writer** | Writes conversational scripts with scene breakdowns |
| **Graphic Designer** | Generates/finds images for each scene |
| **Video Producer** | Creates TTS audio, composes video with FFmpeg |

### Process

1. You send: \`/new top 5 deadliest animals\`
2. **Content Director** receives the task
3. Delegates to **Script Writer** → writes script with voiceover, countdowns, transitions
4. Reviews script (rejects if robotic or missing countdowns)
5. Delegates to **Graphic Designer** → generates DALL-E images + Pexels stock photos
6. Reviews images
7. Delegates to **Video Producer** → generates TTS audio, creates Ken Burns clips, countdowns, merges, adds polish
8. Returns the final video
9. You get notified: \`Task YTS-5 completed!\`
10. \`/result\` to get the video file

### Customization

- **Voice**: OpenAI TTS voices (onyx, echo, nova, coral, etc.)
- **Style**: Per-scene voice direction for natural delivery
- **Countdown**: Automatic for list topics (Top 5, Top 10)
- **Transitions**: 46+ types (fade, slideup, dissolve, etc.)
- **Ken Burns**: Zoom in/out, pan left/right with shake-free 4x pre-scale

### Tools

- **Video Toolkit** workflow: countdown generation, scene clips, merging, polish
- **FFmpeg Guide** skill: comprehensive FFmpeg knowledge
- **OpenAI TTS** skill: text-to-speech
- **Image Generation** skill: DALL-E 3
`
    }
  ];
</script>

<div class="p-6 max-w-4xl mx-auto">
  <div class="flex items-center gap-2 mb-6">
    <BookOpen size={16} class="text-gray-500 dark:text-dark-text-muted" />
    <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Guides</h2>
  </div>

  <div class="space-y-3">
    {#each guides as guide}
      <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
        <!-- Header -->
        <button
          onclick={() => toggle(guide.id)}
          class="w-full flex items-center gap-3 px-4 py-3 text-left hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
        >
          {#if openSections[guide.id]}
            <ChevronDown size={14} class="text-gray-400 shrink-0" />
          {:else}
            <ChevronRight size={14} class="text-gray-400 shrink-0" />
          {/if}
          <div>
            <div class="text-sm font-medium text-gray-900 dark:text-dark-text">{guide.title}</div>
            <div class="text-xs text-gray-500 dark:text-dark-text-muted">{guide.description}</div>
          </div>
        </button>

        <!-- Content -->
        {#if openSections[guide.id]}
          <div class="px-6 pb-6 pt-2 border-t border-gray-100 dark:border-dark-border">
            <div class="prose prose-sm dark:prose-invert max-w-none prose-pre:bg-gray-50 dark:prose-pre:bg-dark-base prose-code:text-[12px] prose-table:text-xs" use:renderMarkdown>
              {@html md(guide.content.trim())}
            </div>
          </div>
        {/if}
      </div>
    {/each}
  </div>
</div>
