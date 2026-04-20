<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import {
    BookOpen,
    Mic,
    Box,
    Send,
    Film,
    Search,
    ChevronRight,
    X,
    Link2,
    Plus,
    Pencil,
    Trash2,
    Save,
    Lock,
    FileText,
    Code,
    Terminal,
    Image,
    Database,
    Cpu,
    Bot,
    Wrench,
    Lightbulb,
    Rocket,
    Shield,
    Zap,
    Package,
    GitBranch,
    Workflow,
    Key,
    Globe,
    Activity,
    Loader2,
    FileCode,
  } from 'lucide-svelte';
  import { renderMarkdown, highlightCode } from '@/lib/helper/markdown';
  import Markdown from '@/lib/components/Markdown.svelte';
  import { push, querystring } from 'svelte-spa-router';
  import { addToast } from '@/lib/store/toast.svelte';
  import { tick } from 'svelte';

  // Markdown rendering is delegated to @/lib/components/Markdown.svelte,
  // which handles parsing + mermaid + copy-button / table-wrapper
  // enhancements in one place via the shared helpers.
  import {
    listGuides,
    createGuide,
    updateGuide,
    deleteGuide,
    type Guide as UserGuide,
  } from '@/lib/api/guides';

  storeNavbar.title = 'Guides';

  // ─── Icon map: lucide name → component ───
  // Used to render icons stored by name in the DB for user guides, and
  // to populate the icon picker in the editor.
  // Type is inferred so lucide's component type is preserved without
  // clashing with Svelte 5's Component<> generic.
  const iconMap = {
    BookOpen,
    FileText,
    Code,
    Terminal,
    Mic,
    Box,
    Send,
    Film,
    Image,
    Database,
    Cpu,
    Bot,
    Wrench,
    Lightbulb,
    Rocket,
    Shield,
    Zap,
    Package,
    GitBranch,
    Workflow,
    Key,
    Globe,
    Activity,
  };
  const iconNames = Object.keys(iconMap) as (keyof typeof iconMap)[];

  function iconFor(name: string) {
    return iconMap[name as keyof typeof iconMap] ?? BookOpen;
  }

  // ─── Guide shape (merged built-in + user) ───
  interface DisplayGuide {
    id: string;
    title: string;
    description: string;
    iconName: string;
    content: string;
    builtin: boolean;
  }

  // ─── Built-in guides (hardcoded, shipped with the app) ───
  const builtinGuides: DisplayGuide[] = [
    {
      id: 'whisper',
      title: 'Speech-to-Text (Whisper)',
      description: 'Voice message transcription for Telegram bots and agents',
      iconName: 'Mic',
      builtin: true,
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
`,
    },
    {
      id: 'containers',
      title: 'Container Isolation',
      description: 'Isolate agent execution with Docker containers',
      iconName: 'Box',
      builtin: true,
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
`,
    },
    {
      id: 'telegram',
      title: 'Telegram Bot Commands',
      description: 'All available commands for Telegram bots',
      iconName: 'Send',
      builtin: true,
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
`,
    },
  ];

  // ─── State ───
  let userGuides = $state<UserGuide[]>([]);
  let loadingUser = $state(true);
  let searchQuery = $state('');
  let selectedId = $state<string>('');
  let contentEl = $state<HTMLElement | null>(null);
  let initialized = false;

  // Editor state
  type Mode = 'view' | 'edit' | 'new';
  let mode = $state<Mode>('view');
  let editingId = $state<string | null>(null);

  // Viewer sub-mode: rendered markdown vs raw source
  let viewMode = $state<'rendered' | 'source'>('rendered');
  let draftTitle = $state('');
  let draftDescription = $state('');
  let draftIcon = $state('BookOpen');
  let draftContent = $state('');
  let saving = $state(false);
  let deleteConfirm = $state<string | null>(null);

  // ─── Derived ───
  const allGuides = $derived<DisplayGuide[]>([
    ...builtinGuides,
    ...userGuides.map<DisplayGuide>((g) => ({
      id: g.id,
      title: g.title || '(untitled)',
      description: g.description || '',
      iconName: g.icon || 'BookOpen',
      content: g.content || '',
      builtin: false,
    })),
  ]);

  const filteredGuides = $derived.by(() => {
    const q = searchQuery.trim().toLowerCase();
    if (!q) return allGuides;
    return allGuides.filter(
      (g) =>
        g.title.toLowerCase().includes(q) ||
        g.description.toLowerCase().includes(q) ||
        g.content.toLowerCase().includes(q),
    );
  });

  const filteredBuiltins = $derived(filteredGuides.filter((g) => g.builtin));
  const filteredUser = $derived(filteredGuides.filter((g) => !g.builtin));

  const selectedGuide = $derived(
    allGuides.find((g) => g.id === selectedId) ?? allGuides[0],
  );

  // ─── Load user guides from API ───
  async function loadUserGuides() {
    loadingUser = true;
    try {
      const res = await listGuides();
      userGuides = res.data ?? [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load guides', 'alert');
    } finally {
      loadingUser = false;
    }
  }

  // ─── Sync from URL query string (?g=<id>) ───
  // Initial selection reads from querystring; after init, selectedId drives the URL.
  $effect(() => {
    const qs = new URLSearchParams($querystring || '');
    const g = qs.get('g');
    if (!initialized) {
      initialized = true;
      if (g && allGuides.some((x) => x.id === g)) {
        selectedId = g;
      } else {
        selectedId = allGuides[0]?.id ?? '';
      }
    } else if (g && allGuides.some((x) => x.id === g) && g !== selectedId) {
      // Handle back/forward navigation
      selectedId = g;
    }
  });

  // Load user guides on mount
  $effect(() => {
    loadUserGuides();
  });

  // ─── Actions ───
  async function selectGuide(id: string) {
    if (id === selectedId && mode === 'view') return;
    if (mode !== 'view') {
      if (!confirm('Discard unsaved changes?')) return;
      mode = 'view';
      editingId = null;
    }
    selectedId = id;
    viewMode = 'rendered';
    push(`/guides?g=${id}`);
    await tick();
    if (contentEl) contentEl.scrollTop = 0;
  }

  function toggleSource() {
    viewMode = viewMode === 'source' ? 'rendered' : 'source';
  }

  function openNew() {
    if (mode !== 'view' && !confirm('Discard unsaved changes?')) return;
    mode = 'new';
    editingId = null;
    draftTitle = '';
    draftDescription = '';
    draftIcon = 'BookOpen';
    draftContent = '# New Guide\n\nStart writing your guide in markdown here.\n';
  }

  function openEdit(guide: DisplayGuide) {
    if (guide.builtin) return;
    mode = 'edit';
    editingId = guide.id;
    draftTitle = guide.title;
    draftDescription = guide.description;
    draftIcon = guide.iconName;
    draftContent = guide.content;
  }

  function cancelEdit() {
    mode = 'view';
    editingId = null;
  }

  async function saveDraft() {
    if (!draftTitle.trim()) {
      addToast('Title is required', 'alert');
      return;
    }
    saving = true;
    try {
      const payload = {
        title: draftTitle.trim(),
        description: draftDescription.trim(),
        icon: draftIcon,
        content: draftContent,
      };
      if (editingId) {
        const updated = await updateGuide(editingId, payload);
        userGuides = userGuides.map((g) => (g.id === editingId ? updated : g));
        selectedId = updated.id;
        addToast('Guide updated');
      } else {
        const created = await createGuide(payload);
        userGuides = [...userGuides, created];
        selectedId = created.id;
        push(`/guides?g=${created.id}`);
        addToast('Guide created');
      }
      mode = 'view';
      editingId = null;
      await tick();
      if (contentEl) contentEl.scrollTop = 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save guide', 'alert');
    } finally {
      saving = false;
    }
  }

  async function confirmDelete(id: string) {
    try {
      await deleteGuide(id);
      userGuides = userGuides.filter((g) => g.id !== id);
      deleteConfirm = null;
      // Move to the first guide if we deleted the current one
      if (selectedId === id) {
        const first = allGuides[0];
        if (first) {
          selectedId = first.id;
          push(`/guides?g=${first.id}`);
        }
      }
      addToast('Guide deleted');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete guide', 'alert');
    }
  }

  async function copyLink() {
    if (!selectedGuide) return;
    const url = `${window.location.origin}${window.location.pathname}#/guides?g=${selectedGuide.id}`;
    try {
      await navigator.clipboard.writeText(url);
      addToast('Link copied to clipboard');
    } catch {
      addToast('Failed to copy link', 'alert');
    }
  }
</script>

<svelte:head>
  <title>AT | Guides</title>
</svelte:head>

<div class="flex h-full">
  <!-- Guide list (nested sidebar) -->
  <aside
    class="w-64 shrink-0 border-r border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface flex flex-col min-h-0"
  >
    <div class="px-3 py-3 border-b border-gray-200 dark:border-dark-border">
      <div class="flex items-center gap-2 mb-3">
        <BookOpen size={14} class="text-gray-500 dark:text-dark-text-muted" />
        <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Guides</h2>
        <span class="ml-auto text-[10px] text-gray-400 dark:text-dark-text-muted tabular-nums">
          {filteredGuides.length}/{allGuides.length}
        </span>
      </div>
      <div class="relative mb-2">
        <Search
          size={12}
          class="absolute left-2 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-text-muted pointer-events-none"
        />
        <input
          type="text"
          bind:value={searchQuery}
          placeholder="Search guides..."
          class="w-full pl-7 pr-7 h-7 text-xs border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated text-gray-900 dark:text-dark-text placeholder:text-gray-400 dark:placeholder:text-dark-text-muted focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors"
        />
        {#if searchQuery}
          <button
            onclick={() => (searchQuery = '')}
            class="absolute right-1 top-1/2 -translate-y-1/2 p-0.5 text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary"
            aria-label="Clear search"
          >
            <X size={12} />
          </button>
        {/if}
      </div>
      <button
        onclick={openNew}
        class="w-full flex items-center justify-center gap-1.5 h-7 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors"
      >
        <Plus size={12} />
        New Guide
      </button>
    </div>

    <nav class="flex-1 overflow-y-auto no-scrollbar">
      <!-- Built-in section -->
      {#if filteredBuiltins.length > 0}
        <div class="flex items-center gap-1.5 px-3 py-1.5 bg-gray-50 dark:bg-dark-base border-b border-gray-200 dark:border-dark-border">
          <Lock size={10} class="text-gray-400 dark:text-dark-text-muted" />
          <span class="text-[10px] font-medium text-gray-400 dark:text-dark-text-muted tracking-wider uppercase">
            Built-in
          </span>
        </div>
        {#each filteredBuiltins as guide (guide.id)}
          {@const Icon = iconFor(guide.iconName)}
          {@const active = selectedId === guide.id && mode === 'view'}
          <button
            onclick={() => selectGuide(guide.id)}
            class={[
              'w-full flex items-start gap-2 px-3 py-2 text-left border-b border-gray-100 dark:border-dark-border transition-colors group',
              active
                ? 'bg-gray-900 text-white dark:bg-accent dark:text-white'
                : 'text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated',
            ]}
          >
            <Icon size={14} class="shrink-0 mt-0.5" />
            <div class="min-w-0 flex-1">
              <div class="text-xs font-medium truncate">{guide.title}</div>
              <div
                class={[
                  'text-[10px] truncate mt-0.5 leading-snug',
                  active
                    ? 'text-gray-300 dark:text-gray-200'
                    : 'text-gray-500 dark:text-dark-text-muted',
                ]}
              >
                {guide.description}
              </div>
            </div>
            <ChevronRight
              size={12}
              class="shrink-0 mt-1 transition-opacity {active
                ? 'opacity-100'
                : 'opacity-0 group-hover:opacity-40'}"
            />
          </button>
        {/each}
      {/if}

      <!-- User guides section -->
      <div class="flex items-center gap-1.5 px-3 py-1.5 bg-gray-50 dark:bg-dark-base border-b border-gray-200 dark:border-dark-border">
        <Pencil size={10} class="text-gray-400 dark:text-dark-text-muted" />
        <span class="text-[10px] font-medium text-gray-400 dark:text-dark-text-muted tracking-wider uppercase">
          My Guides
        </span>
        {#if loadingUser}
          <Loader2 size={10} class="ml-auto animate-spin text-gray-400 dark:text-dark-text-muted" />
        {:else}
          <span class="ml-auto text-[10px] text-gray-400 dark:text-dark-text-muted tabular-nums">
            {filteredUser.length}
          </span>
        {/if}
      </div>

      {#each filteredUser as guide (guide.id)}
        {@const Icon = iconFor(guide.iconName)}
        {@const active = selectedId === guide.id && mode === 'view'}
        <button
          onclick={() => selectGuide(guide.id)}
          class={[
            'w-full flex items-start gap-2 px-3 py-2 text-left border-b border-gray-100 dark:border-dark-border transition-colors group',
            active
              ? 'bg-gray-900 text-white dark:bg-accent dark:text-white'
              : 'text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated',
          ]}
        >
          <Icon size={14} class="shrink-0 mt-0.5" />
          <div class="min-w-0 flex-1">
            <div class="text-xs font-medium truncate">{guide.title}</div>
            <div
              class={[
                'text-[10px] truncate mt-0.5 leading-snug',
                active
                  ? 'text-gray-300 dark:text-gray-200'
                  : 'text-gray-500 dark:text-dark-text-muted',
              ]}
            >
              {guide.description || '(no description)'}
            </div>
          </div>
          <ChevronRight
            size={12}
            class="shrink-0 mt-1 transition-opacity {active
              ? 'opacity-100'
              : 'opacity-0 group-hover:opacity-40'}"
          />
        </button>
      {/each}

      {#if !loadingUser && filteredUser.length === 0 && !searchQuery}
        <div class="p-4 text-center">
          <div class="text-[11px] text-gray-500 dark:text-dark-text-muted leading-relaxed">
            No user guides yet.<br />
            Click <span class="font-medium text-gray-700 dark:text-dark-text-secondary">+ New Guide</span> to create one.
          </div>
        </div>
      {/if}

      {#if filteredGuides.length === 0 && searchQuery}
        <div class="p-6 text-center">
          <Search size={20} class="mx-auto text-gray-300 dark:text-dark-text-muted mb-2" />
          <div class="text-xs text-gray-500 dark:text-dark-text-muted">
            No guides match "{searchQuery}"
          </div>
          <button
            onclick={() => (searchQuery = '')}
            class="mt-2 text-[11px] text-gray-700 dark:text-dark-text-secondary hover:underline"
          >
            Clear search
          </button>
        </div>
      {/if}
    </nav>
  </aside>

  <!-- Content pane -->
  <div
    bind:this={contentEl}
    class="flex-1 overflow-y-auto bg-gray-50 dark:bg-dark-base min-h-0"
  >
    {#if mode === 'view'}
      <!-- ─── Viewer ─── -->
      {#if selectedGuide}
        {@const Icon = iconFor(selectedGuide.iconName)}
        <div class="max-w-6xl mx-auto px-6 md:px-10 py-6 md:py-8">
          <!-- Header -->
          <header class="mb-8 pb-6 border-b border-gray-200 dark:border-dark-border">
            <div class="flex items-start gap-4">
              <div
                class="w-10 h-10 flex items-center justify-center bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border shrink-0"
              >
                <Icon size={18} class="text-gray-700 dark:text-dark-text-secondary" />
              </div>
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-2 flex-wrap">
                  <h1 class="text-lg font-semibold text-gray-900 dark:text-dark-text">
                    {selectedGuide.title}
                  </h1>
                  {#if selectedGuide.builtin}
                    <span class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] font-medium bg-gray-100 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border text-gray-600 dark:text-dark-text-secondary">
                      <Lock size={9} />
                      Built-in
                    </span>
                  {/if}
                </div>
                {#if selectedGuide.description}
                  <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-1">
                    {selectedGuide.description}
                  </p>
                {/if}
              </div>
              <div class="flex items-center gap-1.5 shrink-0">
                <button
                  onclick={toggleSource}
                  class={[
                    'flex items-center gap-1.5 px-2 py-1 text-[11px] border transition-colors',
                    viewMode === 'source'
                      ? 'text-white bg-gray-900 dark:bg-accent dark:text-white border-gray-900 dark:border-accent'
                      : 'text-gray-600 dark:text-dark-text-secondary border-gray-300 dark:border-dark-border-subtle hover:bg-gray-100 dark:hover:bg-dark-elevated',
                  ]}
                  title={viewMode === 'source' ? 'Show rendered markdown' : 'Show raw markdown source'}
                >
                  <FileCode size={11} />
                  Source
                </button>
                <button
                  onclick={copyLink}
                  class="flex items-center gap-1.5 px-2 py-1 text-[11px] text-gray-600 dark:text-dark-text-secondary border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors"
                  title="Copy link to this guide"
                >
                  <Link2 size={11} />
                  Copy link
                </button>
                {#if !selectedGuide.builtin}
                  <button
                    onclick={() => openEdit(selectedGuide)}
                    class="flex items-center gap-1.5 px-2 py-1 text-[11px] text-gray-600 dark:text-dark-text-secondary border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors"
                    title="Edit guide"
                  >
                    <Pencil size={11} />
                    Edit
                  </button>
                  {#if deleteConfirm === selectedGuide.id}
                    <button
                      onclick={() => confirmDelete(selectedGuide.id)}
                      class="flex items-center gap-1.5 px-2 py-1 text-[11px] text-white bg-red-600 hover:bg-red-700 border border-red-600 transition-colors"
                      title="Confirm delete"
                    >
                      <Trash2 size={11} />
                      Confirm
                    </button>
                    <button
                      onclick={() => (deleteConfirm = null)}
                      class="flex items-center gap-1.5 px-2 py-1 text-[11px] text-gray-600 dark:text-dark-text-secondary border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors"
                    >
                      Cancel
                    </button>
                  {:else}
                    <button
                      onclick={() => (deleteConfirm = selectedGuide.id)}
                      class="flex items-center gap-1.5 px-2 py-1 text-[11px] text-red-600 dark:text-red-400 border border-red-300 dark:border-red-900 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors"
                      title="Delete guide"
                    >
                      <Trash2 size={11} />
                      Delete
                    </button>
                  {/if}
                {/if}
              </div>
            </div>
          </header>

          <!-- Markdown content -->
          {#if viewMode === 'source'}
            <article
              class="markdown-body max-w-none text-[13.5px] leading-[1.7]"
              use:renderMarkdown
            >
              <pre><code class="hljs language-markdown">{@html highlightCode(
                  selectedGuide.content.trim(),
                  'markdown',
                )}</code></pre>
            </article>
          {:else}
            <Markdown
              source={selectedGuide.content.trim()}
              as="article"
              class="max-w-none text-[13.5px] leading-[1.7]"
              enhance
            />
          {/if}
        </div>
      {:else}
        <div class="h-full flex items-center justify-center text-sm text-gray-500 dark:text-dark-text-muted">
          Select a guide from the sidebar
        </div>
      {/if}
    {:else}
      <!-- ─── Editor (split view) ─── -->
      {@const EditorIcon = iconFor(draftIcon)}
      <div class="h-full flex flex-col min-h-0">
        <!-- Editor header -->
        <div class="flex items-center justify-between px-4 py-2 border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface shrink-0">
          <div class="flex items-center gap-2">
            <EditorIcon size={14} class="text-gray-500 dark:text-dark-text-muted" />
            <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
              {mode === 'new' ? 'New Guide' : `Edit: ${draftTitle || '(untitled)'}`}
            </span>
          </div>
          <div class="flex items-center gap-2">
            <button
              onclick={cancelEdit}
              disabled={saving}
              class="flex items-center gap-1.5 px-3 py-1 text-xs text-gray-600 dark:text-dark-text-secondary border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors disabled:opacity-50"
            >
              Cancel
            </button>
            <button
              onclick={saveDraft}
              disabled={saving || !draftTitle.trim()}
              class="flex items-center gap-1.5 px-3 py-1 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
            >
              {#if saving}
                <Loader2 size={12} class="animate-spin" />
              {:else}
                <Save size={12} />
              {/if}
              Save
            </button>
          </div>
        </div>

        <!-- Metadata form -->
        <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface shrink-0 grid grid-cols-[1fr_auto] gap-3">
          <div class="space-y-2">
            <div class="grid grid-cols-[5rem_1fr] gap-2 items-center">
              <label for="guide-title" class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Title</label>
              <input
                id="guide-title"
                type="text"
                bind:value={draftTitle}
                placeholder="My Guide"
                class="border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 text-xs focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>
            <div class="grid grid-cols-[5rem_1fr] gap-2 items-center">
              <label for="guide-description" class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
              <input
                id="guide-description"
                type="text"
                bind:value={draftDescription}
                placeholder="One-line summary"
                class="border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 text-xs focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>
          </div>
          <div class="grid grid-cols-[3rem_8rem] gap-2 items-center">
            <label for="guide-icon" class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Icon</label>
            <select
              id="guide-icon"
              bind:value={draftIcon}
              class="border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 text-xs focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text"
            >
              {#each iconNames as name}
                <option value={name}>{name}</option>
              {/each}
            </select>
          </div>
        </div>

        <!-- Split view: markdown editor | preview -->
        <div class="flex-1 flex min-h-0">
          <!-- Markdown editor -->
          <div class="flex-1 flex flex-col min-h-0 border-r border-gray-200 dark:border-dark-border">
            <div class="px-3 py-1.5 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-[10px] font-medium text-gray-500 dark:text-dark-text-muted tracking-wider uppercase shrink-0">
              Markdown
            </div>
            <textarea
              bind:value={draftContent}
              placeholder="# Start writing your guide..."
              spellcheck="false"
              class="flex-1 p-4 text-[12px] font-mono leading-relaxed bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder:text-gray-400 dark:placeholder:text-dark-text-muted resize-none focus:outline-none min-h-0"
            ></textarea>
          </div>

          <!-- Live preview -->
          <div class="flex-1 flex flex-col min-h-0">
            <div class="px-3 py-1.5 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-[10px] font-medium text-gray-500 dark:text-dark-text-muted tracking-wider uppercase shrink-0">
              Preview
            </div>
            <div class="flex-1 overflow-y-auto px-5 py-4 bg-gray-50 dark:bg-dark-base min-h-0">
              {#if draftContent.trim()}
                <Markdown
                  source={draftContent}
                  as="article"
                  class="max-w-none text-[13.5px] leading-[1.7]"
                  enhance
                />
              {:else}
                <div class="text-xs text-gray-400 dark:text-dark-text-muted italic">
                  Preview will appear here as you type.
                </div>
              {/if}
            </div>
          </div>
        </div>
      </div>
    {/if}
  </div>
</div>

<style>
  /* Hide scrollbar for nested guide list */
  .no-scrollbar::-webkit-scrollbar {
    display: none;
  }
  .no-scrollbar {
    -ms-overflow-style: none;
    scrollbar-width: none;
  }

  /* All markdown typography — including inline code, code blocks with copy
     button + language label, and tables with horizontal scroll — is now
     centralised in global.css under `.markdown-body` so every md() call
     site renders identically. */
</style>
