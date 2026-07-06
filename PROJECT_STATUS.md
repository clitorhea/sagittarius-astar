# AI Handover Context & Project Status
**Project Name:** `aig` (sagittarius-a*)
**Type:** Terminal-based AI Assistant (TUI)
**Stack:** Go, Bubble Tea, Lipgloss, Glamour, x/sys/windows

## 🎯 Purpose of this file
This file serves as a compressed "brain dump" for future AI agents working on this project. Read this before starting a task to understand the architecture, recent changes, and known quirks, thereby minimizing token usage on redundant codebase discovery.

---

## 🏗️ Architecture & Core Modules
- **`internal/tui/`**: The heart of the application. Uses Bubble Tea's Elm architecture.
  - `model.go`: Contains the main state machine (`Model`), initialization, and command streaming logic.
  - *Key States*: `stateInput` (normal chat), `stateStreaming` (LLM is typing), `stateExecutingTool` / `stateExecuting` (sandbox commands), `stateRuntimeInput` (injecting stdin mid-execution).
- **`internal/sandbox/`**: Executes OS commands. Uses `io.Pipe` to feed stdin dynamically.
- **`internal/llm/`**: Abstractions for AI providers (Gemini, DeepSeek).
- **`internal/config/`**: Configuration management. Loads from `~/.config/aig/config.json` or Environment Variables (ENV vars *always* take precedence).

---

## 🕒 Recent Implementations & Fixes

1. **Model Context Protocol (MCP) Host Integration**
   - **Subprocess Manager (`internal/mcp/manager.go`)**: Manages the lifecycle of MCP servers via standard I/O pipes. Supports SIGINT-to-SIGKILL graceful termination within 5 seconds on exit.
   - **Multiplexed Client (`internal/mcp/client.go`)**: Multiplexes concurrent requests asynchronously. Features an enlarged 10 MiB scan buffer for massive directory/file returns.
   - **Tool Registry (`internal/mcp/registry.go`)**: Performs dynamic initialization handshakes, runs `tools/list` on start, translates JSON schemas into LLM-compatible shapes, and routes tool calls to the correct client.
   - **TUI Integration**: Added `mcpToolCallCmd` to run MCP calls in non-blocking Bubble Tea commands while maintaining TUI interactivity.

2. **Advanced Reasoning Streaming & Rendering**
   - **Real-Time Thought Streaming**: Upgraded Gemini and DeepSeek providers to stream reasoning tokens immediately, prefixed with a null byte (`\x00`).
   - **Visual Rendering**: Created a distinct styled display for reasoning (italicized, left-border indented Catppuccin palette).
   - **StatusBar Toggle**: Toggles status text to `● deliberating` when the model is in its thinking cycle.
   - **Plan-Action-Reflection (PAR) Loop**: Dynamically injects planning and reflection instructions into system prompts when tools are active.

3. **Loop Breaker Circuit**
   - **Consecutive Same-Call Breaker**: Intercepts tool execution if the exact same tool and argument hash are called 3 times consecutively. Halts the execution loop and injects a warning context to force model re-evaluation.
   - **Consecutive Error Breaker**: Intercepts and aborts tool loops after 3 consecutive failures to prevent cascading resource waste.

4. **Dynamic API Key Reloading & Windows Selection**
   - **API Key Resolving**: Resolves keys dynamically on each generation run rather than caching on start.
   - **Quick Edit Mode**: Restores Quick Edit Mode on Windows Terminal to enable native selection without breaking the TUI.

---

## ⚠️ Crucial Rules & Quirks for Future AIs

- **Reasoning Prefix (`\x00`)**: Do not remove the null byte prefix from streamed reasoning tokens. The TUI relies on this prefix to route tokens to `reasoningBuffer` instead of `streamBuffer`.
- **MCP Server Graces**: When adding or managing MCP servers, ensure `Registry.Shutdown()` is called *before* `Manager.Shutdown()` to drain pending requests safely before closing the pipes.
- **Bubble Tea State Changes**: Whenever the `appState` changes to something that alters the UI height (like opening the Runtime Input box), you MUST ensure `m.recalculateDimensions()` is called so the viewport resizes correctly.
- **Loop Breaker State Resets**: Ensure `consecutiveSameCalls` and `consecutiveErrors` are correctly reset upon tool execution successes or state changes to prevent false positives.
- **Dynamic Context Injection**: Dynamic system instructions (like the PAR loop directive) should only be injected into the *outgoing* history array during streaming, keeping the session database/history clean.
