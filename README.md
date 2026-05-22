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

- 🤖 **Multi-provider** — Gemini (`gemini-2.0-flash`, `gemini-2.5-pro`, …) and DeepSeek (`deepseek-chat`, `deepseek-reasoner`)
- 🌊 **Streaming TUI** — real-time token streaming via Bubble Tea, non-blocking keyboard listener
- 📝 **Markdown rendering** — responses rendered through Glamour with dark/light theme auto-detection
- ⚡ **Secure execution** — detects `bash`/`sh`/`ps1` code blocks, asks for consent, pipes output back to conversation
- 🖥️ **Cross-platform** — Linux (bash) and Windows (PowerShell) command execution
- 🎨 **Premium aesthetics** — Catppuccin Mocha palette, rounded borders, smooth status indicators

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
aig --provider deepseek --model deepseek-reasoner
aig -p deepseek -m deepseek-chat

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
| `/help` | Display list of available commands |
| `/history` | List all saved conversation sessions |
| `/load <id>` | Resume a conversation by its session ID |
| `/save <name>` | Save/rename current session with a friendly name |
| `/new` | Start a fresh conversation thread |
| `/clear` | Clear viewport display (keeps system prompt) |
| `/quit`, `/q` | Exit the application |
| `Ctrl+C` | Cancel stream or quit |
| `Enter` | Send message |
| `Shift+Enter` | Insert newline in text area |

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
INPUT ──[submit]──► STREAMING ──[done]──► INPUT
                        │
                   [code block found]
                        │
                        ▼
               CONFIRMING_CMD ──[y]──► EXECUTING_CMD ──► INPUT
                        │                                  ▲
                      [n/Esc]                               │
                        └──────────────────────────────────┘
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
