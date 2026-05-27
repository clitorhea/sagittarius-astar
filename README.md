# Sagittarius A\* — `aig`

> A cross-platform, terminal-based AI agent written in Go.

````
  ✦ aig — Sagittarius A*
  terminal AI agent  ·  type /help for commands
  ──────────────────────────────────────────────
  You
  what files are in this directory?

  aig
  I'll list the files for you.
  ```bash
  ls -la
````

[Execute this bash block? (y/N)]

````

---

## Features

### Core Capabilities
- 🤖 **Multi-Provider & Model Integration** — Support for Google Gemini (official Go SDK, including `gemini-2.0-flash`, `gemini-2.5-pro`, etc.) and DeepSeek (OpenAI-compatible, including `deepseek-v4-flash`, `deepseek-v4-pro`, etc.). Switch providers and models on-the-fly inside the active session.
- ⚡ **Autonomous Tool-Calling Agent Loop** — Runs multi-turn tool loops sequentially to resolve complex tasks (e.g., read file, search, write/edit file, run test command, repeat).
- 🌊 **Real-time Streaming TUI** — Built on Bubble Tea and Bubbles, featuring live token streaming, interactive spinners, and status bar updates.
- 🎨 **Premium Aesthetics** — Custom Catppuccin Mocha themed UI, rounded borders, and dynamic status bars highlighting current state, active model, persona, and auto-approve mode status.
- 📝 **Live Markdown Rendering** — Renders markdown responses on-the-fly using Glamour with automatic dark/light theme detection.
- 🖥️ **Cross-Platform** — Native shell/sandbox support for Linux (bash/sh) and Windows (PowerShell) execution.

### Integrated Agent Tools
The agent can autonomously invoke these tools:
- 📂 **File Utilities**:
  - `read_file` — Reads entire files or targeted line-ranges (`start_line`/`end_line`) with a 50KB safety cap.
  - `write_file` — Overwrites or creates new files (prompts user with a unified diff preview).
  - `edit_file` — Surgery-grade text replacements using exact old/new snippets (verifies single-match uniqueness and displays a diff).
  - `list_directory` — Lists entries with file-size information (similar to `ls`).
- 🔍 **Search & Mapping**:
  - `search_regex` — Runs recursive regex matching across files (similar to `grep -rn`) with path, line-number, and glob filtering.
  - `map_workspace` — Generates a tree layout of the workspace to help orient the agent.
- 🌐 **Web Scraper & Search**:
  - `web_search` — DuckDuckGo query search returning titles, snippets, and source links.
  - `web_fetch` — Fetches raw HTML page text, preserves code blocks, strips noise (scripts, CSS, headers, footers), and formats content for documentation reference.

### Safety & Command Sandboxing
- 🛡️ **Interactive Gating**: Full confirmation dialogs for file mutations (`write_file`, `edit_file`) showing unified diffs.
- ⚡ **Auto-Approve Mode**: Enable auto-approve (`/approve-tools on` or CLI flag) to execute sandbox commands (`run_command`) autonomously without manual confirmation prompts.
- 🛑 **Graceful Interrupts**: Cancel active streams or executing sandbox commands safely using `Ctrl+C` without crashing the application.

---

## Installation

### From Source

```bash
git clone github.com/clitorhea/sagittarius-astar.git
cd sagittarius-astar
go mod tidy
make build          # current OS
make build-all      # linux-amd64 + windows-amd64
./bin/aig
````

### Nix (Development Shell)

```bash
nix develop         # enters the dev shell with Go, gopls, golangci-lint
nix build           # builds the aig package (update vendorHash first)
```

---

## Configuration

`aig` resolves settings from the following sources, in order of priority:
1. CLI Flags (`--provider`, `--model`, `--persona`, etc.)
2. Environment variables (`GEMINI_API_KEY`, `DEEPSEEK_API_KEY`)
3. JSON Configuration File (`~/.config/aig/config.json`)

On first launch, `aig` generates a default `config.json` at:
- **Linux/macOS:** `~/.config/aig/config.json`
- **Windows:** `%AppData%\aig\config.json`

### `config.json` Structure
```json
{
  "default_provider": "gemini",
  "default_model": "",
  "gemini_api_key": "YOUR_GEMINI_KEY",
  "deepseek_api_key": "YOUR_DEEPSEEK_KEY",
  "personas": {
    "default": "You are aig...",
    "sysadmin": "You are an expert Linux and Windows system administrator...",
    "coder": "You are an expert software engineer..."
  }
}
```

---

## Usage

```bash
# Default (Gemini)
aig

# Specify provider and model
aig --provider deepseek --model deepseek-v4-pro
aig -p deepseek -m deepseek-v4-flash

# Use a custom system prompt persona
aig --persona sysadmin
aig -s coder

# Resume a previous conversation by ID
aig --resume 20260521-170000
aig -r 20260521-170000
```

### In-session commands

| Command | Action |
| ------------- | ------------------------------ |
| `/help` or `/?` | Display help menu listing all commands and shortcuts |
| `/history` | List all saved conversation sessions with timestamps and names |
| `/load <id>` | Resume a conversation by its session ID |
| `/save <name>` or `/rename <name>` | Save/rename the current session with a friendly name |
| `/delete <id>` | Permanently delete a saved conversation |
| `/new` | Start a fresh conversation thread, resetting history but preserving the persona |
| `/clear` | Clear the viewport display while retaining the system prompt |
| `/model` | List available models for the current active provider |
| `/model <id>` | Switch the active model on-the-fly (keeps conversation history) |
| `/provider` | List available providers |
| `/provider <name>` | Switch the active provider on-the-fly (keeps conversation history) |
| `/persona` | List available personas configured |
| `/persona <name>` | Switch the system prompt/persona on-the-fly (takes effect on next message) |
| `/approve-tools on\|off` | Toggle auto-approve mode for commands (shows `⚡auto` badge in status bar when active) |
| `/map [dir]` | Generates and injects a tree-like map of `[dir]` into the conversation history |
| `/quit` or `/q` | Exit the application |
| `Ctrl+C` | Cancel a running command / token stream, or quit |
| `Enter` | Send message |
| `Shift+Enter` | Insert a newline in the text area |

### Inline Macros

- **`/read(path)`**: Attach the content of a file located at `path` directly to the next message context. You can include multiple `/read(...)` macros in a single input message. E.g., `Refactor /read(internal/tui/styles.go) to use Catppuccin theme.`

---

## Architecture

```
cmd/aig/
└── main.go              Cobra CLI entry point

internal/
├── config/
│   └── config.go        Env-based config loading & validation
├── llm/
│   ├── provider.go      Provider interface + Message type
│   ├── gemini.go        google.golang.org/genai streaming
│   └── deepseek.go      sashabaranov/go-openai + DeepSeek BaseURL
├── tui/
│   ├── model.go         Bubble Tea state machine (4 states)
│   ├── messages.go      Custom tea.Msg types
│   └── styles.go        Lipgloss style definitions
└── sandbox/
    └── executor.go      OS-aware command execution
```

### State Machine

```
            ┌───────────────────────────────────────────────┐
            ▼                                               │
 ┌─────────────────────┐                                    │
 │     stateInput      │                                    │
 └──────────┬──────────┘                                    │
            │ [submit]                                      │
            ▼                                               │
 ┌─────────────────────┐                                    │
 │   stateStreaming    ├──────────────[done / no tools]─────┤
 └──────────┬──────────┘                                    │
            │                                               │
      [tool calls]                                          │
            │                                               │
            ▼                                               │
 ┌─────────────────────┐                                    │
 │ stateExecutingTools │                                    │
 └──────────┬──────────┘                                    │
            │                                               │
            ├───────────────► [Read/Search Tool]            │
            │                 Executes immediately          │
            │                                               │
            ├───────────────► [File Mutation Tool]          │
            │                 └─► stateConfirmingWrite ──┐  │
            │                     ├─► [y] ──► Commit ────┤  │
            │                     └─► [n] ──► Skip ──────┤  │
            │                                            │  │
            └───────────────► [run_command Tool]         │  │
                              ├─► Auto-approve ──────────┼──┤
                              └─► Confirm Gate ──────────┘  │
                                  └─► stateConfirmingCmd    │
                                      ├─► [y]               │
                                      │   └─► stateExecuting│
                                      │       └─► Execute ──┤
                                      └─► [n] ──► Skip ─────┘
```

### Streaming Architecture

The TUI uses the **`waitForToken` relay pattern**:

1. On submit, a background goroutine calls `provider.StreamChat()` and writes tokens into a buffered channel.
2. `waitForToken(ch)` is a `tea.Cmd` that blocks on `<-ch` and returns one `tokenMsg`.
3. On receiving `tokenMsg`, `Update` appends the token and re-issues `waitForToken(ch)` — creating a continuous, non-blocking loop.
4. When the channel is closed, `waitForToken` returns `nil` (no-op), and `streamDoneMsg` triggers the final glamour render.

---

## Development

```bash
make test     # go test -race ./...
make vet      # go vet ./...
make fmt      # gofmt -w .
make tidy     # go mod tidy
```

---

## License

MIT
