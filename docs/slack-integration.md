# Slack Integration Setup

## Creating the Slack App

1. Go to [api.slack.com/apps](https://api.slack.com/apps)
2. Click **Create New App** → **From scratch**
3. Name: `agenthub`, pick your workspace

## Required Scopes

### Bot Token Scopes (`xoxb-`)

Navigate to **OAuth & Permissions** → **Scopes** → **Bot Token Scopes**:

| Scope | Purpose |
|-------|---------|
| `chat:write` | Send messages |
| `commands` | Respond to slash commands |
| `app_mentions:read` | Receive @mentions |
| `im:history` | Read DMs sent to the bot |
| `channels:read` | Read channel info for bot binding |

### App-Level Token Scopes (`xapp-`)

Navigate to **Basic Information** → **App-Level Tokens** → **Generate Token**:

| Scope | Purpose |
|-------|---------|
| `connections:write` | Socket Mode connection |

## Enable Socket Mode

1. **Settings** → **Socket Mode** → Enable Socket Mode
2. Copy the App-Level Token (`xapp-...`) — you'll enter this in the agenthub admin UI

## Slash Commands

Navigate to **Slash Commands** → **Create New Command**:

| Command | Description |
|---------|-------------|
| `/agenthub` | Main agenthub command (bind, list, remove, task creation) |

For the Request URL, enter any placeholder (Socket Mode doesn't use an HTTP URL for slash commands, but the field is required in the UI). Example: `https://placeholder.example.com/slack/commands`

## Install the App

1. **OAuth & Permissions** → **Install to Workspace**
2. Copy the Bot User OAuth Token (`xoxb-...`)

## Configure agenthub

After running `./agenthub setup`, log into the admin web UI and navigate to **Secrets**:

1. Enter your `xoxb-` Bot Token
2. Enter your `xapp-` App Token

Then restart the service:
```bash
./agenthub serve
```

## Testing the Integration

In your Slack workspace:
```
/agenthub list
```

You should see a response from agenthub listing registered bots (empty on first use).

## DM Channel

Users can also DM the agenthub bot directly to:
- Create work items
- Check bot status
- Ask questions about the bot ecosystem

The bot uses OpenAI to understand natural language requests.
