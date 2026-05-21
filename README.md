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

`aig` reads API keys from environment variables:

| Provider   | Environment Variable | Default Model      |
| ---------- | -------------------- | ------------------ |
| `gemini`   | `GEMINI_API_KEY`     | `gemini-2.0-flash` |
| `deepseek` | `DEEPSEEK_API_KEY`   | `deepseek-chat`    |

```bash
export GEMINI_API_KEY="your-key-here"
export DEEPSEEK_API_KEY="your-key-here"
```

---

## Usage

```bash
# Default (Gemini)
aig

# Specify provider and model
aig --provider deepseek --model deepseek-reasoner
aig --provider gemini --model gemini-2.5-pro

# Short flags
aig -p deepseek -m deepseek-chat
```

### In-session commands

| Command       | Action                         |
| ------------- | ------------------------------ |
| `/clear`      | Clear conversation and history |
| `/quit`       | Exit `aig`                     |
| `Ctrl+C`      | Cancel stream or quit          |
| `Enter`       | Send message                   |
| `Shift+Enter` | Insert newline in input        |

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
