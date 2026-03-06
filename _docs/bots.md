# Discord & Telegram Bot Integration

AT can connect your agents to Discord and Telegram. Incoming messages are routed to the same agentic loop that powers the Chat UI — the bot runs tools, calls LLMs, and sends back responses.

## How It Works

```
User message (Discord/Telegram)
  → Bot adapter receives message
  → Finds or creates a chat session (by platform + user + channel)
  → Runs the agentic loop (same as Chat UI: LLM → tool calls → LLM → ...)
  → Collects the final text response
  → Sends it back to the platform (chunked if too long)
```

Each unique user+channel combination gets its own persistent chat session, visible in the **Sessions** page with platform metadata. The bot remembers conversation history across messages.

## Prerequisites

Before adding a bot, you need:

1. **A provider** — configured in the Providers page (e.g., OpenAI, Anthropic, Gemini)
2. **An agent** — configured in the Agents page with a provider, model, and optionally skills/tools
3. **A bot token** — obtained from Discord or Telegram (see below)

## Step 1: Get a Bot Token

### Discord

1. Go to the [Discord Developer Portal](https://discord.com/developers/applications)
2. Click **New Application**, give it a name, and create it
3. Go to the **Bot** tab in the left sidebar
4. Click **Reset Token** and copy the token — save it somewhere safe, you won't see it again
5. Under **Privileged Gateway Intents**, enable:
   - **Message Content Intent** (required to read message text)
6. Go to **OAuth2 → URL Generator** in the left sidebar
7. Under **Scopes**, check `bot`
8. Under **Bot Permissions**, check:
   - Send Messages
   - Read Message History
9. Copy the generated URL at the bottom and open it in your browser to invite the bot to your server

### Telegram

1. Open Telegram and message [@BotFather](https://t.me/BotFather)
2. Send `/newbot`
3. Follow the prompts — choose a display name and a username (must end in `bot`)
4. BotFather will reply with your bot token — copy it

## Step 2: Create the Bot in AT

1. Go to the **Bots** page in the AT UI
2. Click **New Bot**
3. Fill in the form:

| Field | Description |
|-------|-------------|
| **Platform** | `discord` or `telegram` |
| **Name** | A label for your reference (e.g., "Support Bot") |
| **Token** | The bot token from Step 1 |
| **Default Agent** | Select which agent handles messages |
| **Enabled** | Toggle on to start the bot immediately |

4. Click **Create**

The bot connects immediately if enabled. Check the server logs for `discord bot started` or `telegram bot started` to confirm.

## Step 3: Talk to Your Bot

### Discord
- Go to any channel where the bot was invited
- Type a message — the bot will show a typing indicator while processing, then reply

### Telegram
- Open a chat with your bot (search for its username)
- Send any message — the bot will show "typing..." while processing, then reply

## Channel/Chat Agent Overrides

By default, every message goes to the **Default Agent**. You can override this per channel:

1. In the bot form, click **Add override** under "Channel Overrides" (Discord) or "Chat Overrides" (Telegram)
2. Enter the channel/chat ID and select a different agent

**Finding IDs:**
- **Discord**: Enable Developer Mode (User Settings → Advanced → Developer Mode), then right-click a channel → Copy Channel ID
- **Telegram**: Use [@userinfobot](https://t.me/userinfobot) or check the chat ID in the URL of Telegram Web (`-100XXXXXXXXXX` for groups)

## How Sessions Work

- Each unique combination of `(platform, user_id, channel_id)` maps to one chat session
- The first message auto-creates the session; subsequent messages continue it
- Sessions appear in the **Sessions** page with platform metadata in the config column
- You can view the full conversation history in the Sessions page
- Deleting a session resets the conversation — the bot will create a new one on the next message

## Message Limits

| Platform | Max message length | Behavior |
|----------|-------------------|----------|
| Discord | 2,000 characters | Long responses are split into multiple messages |
| Telegram | 4,096 characters | Long responses are split into multiple messages |

The bot tries to split at newline boundaries to keep messages readable.

## YAML Configuration (Alternative)

Bots can also be configured via `at.yaml` instead of the UI. YAML-configured bots start alongside DB-configured bots:

```yaml
bots:
  discord:
    token: "YOUR_DISCORD_BOT_TOKEN"
    default_agent_id: "01JXXXXXXXXXXXXXXXXXXXXXX"
    channel_agents:
      "1234567890": "01JYYYYYYYYYYYYYYYYYYYYYYYY"

  telegram:
    token: "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ"
    default_agent_id: "01JXXXXXXXXXXXXXXXXXXXXXX"
    chat_agents:
      "-1001234567890": "01JYYYYYYYYYYYYYYYYYYYYYYYY"
```

Or via environment variables:

```bash
AT_BOTS_DISCORD_TOKEN="YOUR_DISCORD_BOT_TOKEN"
AT_BOTS_DISCORD_DEFAULT_AGENT_ID="01JXXXXXXXXXXXXXXXXXXXXXX"

AT_BOTS_TELEGRAM_TOKEN="123456789:ABCdefGHIjklMNOpqrSTUvwxYZ"
AT_BOTS_TELEGRAM_DEFAULT_AGENT_ID="01JXXXXXXXXXXXXXXXXXXXXXX"
```

## Troubleshooting

| Problem | Solution |
|---------|----------|
| Bot doesn't respond | Check server logs for errors. Verify the token is correct and the bot is enabled. |
| "agent not found" in logs | The Default Agent ID must point to an existing agent. Check the Agents page. |
| Discord: bot doesn't see messages | Enable **Message Content Intent** in the Discord Developer Portal (Bot tab). |
| Discord: bot not in channel | Use the OAuth2 URL to invite the bot to your server. |
| Telegram: bot doesn't receive messages | Make sure you're messaging the bot directly or it's added to the group. |
| Long delay before response | The agentic loop runs tools sequentially. Check if your agent's tools are slow. |
| Bot creates new session each time | Sessions are keyed by `(platform, user_id, channel_id)`. If IDs change, new sessions are created. |

## Architecture

The bot integration reuses the existing chat infrastructure:

- **`RunAgenticLoop`** — the core loop extracted from the HTTP handler, shared by both SSE streaming and bot adapters
- **`findOrCreateBotSession`** — looks up sessions by platform metadata, creates one if missing
- **`collectAgenticResponse`** — runs the loop and collects all text content events into a single string
- **Bot adapters** (`bot_discord.go`, `bot_telegram.go`) — platform-specific message handling, typing indicators, and message chunking
