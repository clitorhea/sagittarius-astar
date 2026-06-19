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

1. **Abortable Executions & Interactive Stdin**
   - **Abort (`Ctrl+C`)**: When a background command or AI tool is executing, pressing `Ctrl+C` triggers `killExec()`, which cancels the context, closes the stdin pipe, and safely returns an error to the LLM so it doesn't hang.
   - **Stdin Injection (`Ctrl+I`)**: Added `stateRuntimeInput`. While a process is running, pressing `Ctrl+I` opens a text box at the bottom. Hitting Enter writes that line directly to the running process's stdin (via `m.stdinPipe`). Useful for SSH password prompts, script inputs, etc.

2. **Dynamic API Key Reloading**
   - **The Problem**: API keys used to be cached on startup. If a user edited `config.json`, the app ignored it.
   - **The Fix**: Removed the `apiKeys` map from the `Model` struct. Now, `config.GetAPIKey()` dynamically resolves the key (checking ENV vars first, then `config.json`). `startStreaming()` recreates the LLM `Provider` right before generation to instantly pick up config edits without a restart.

3. **Windows Native Text Selection (Mouse Copy/Paste)**
   - **The Problem**: Bubble Tea naturally disables Windows' `ENABLE_QUICK_EDIT_MODE` on startup to prevent the terminal from freezing when clicked. However, this completely broke native text selection in Windows Terminal.
   - **The Fix**: Added `internal/tui/console_windows.go` containing `enableWindowsQuickEdit()`. This hook fires during the Model's `Init()` phase, overriding Bubble Tea and forcing Quick Edit Mode back on. Text selection now works on Windows without holding Shift. `console_notwindows.go` acts as a no-op fallback.

---

## ⚠️ Crucial Rules & Quirks for Future AIs

- **Bubble Tea State Changes**: Whenever the `appState` changes to something that alters the UI height (like opening the Runtime Input box), you MUST ensure `m.recalculateDimensions()` is called so the viewport resizes correctly.
- **Pipe Cleanups**: If you modify command execution, ensure `m.stdinPipe` is closed on BOTH ends when the command finishes or is killed to prevent resource leaks and hanging processes.
- **API Keys & ENV Vars**: If a user complains that their config edits aren't working, remind them that `export DEEPSEEK_API_KEY=...` in their shell will *always* silently override the `config.json` file.
- **Windows Quirks**: If modifying terminal input handling, remember we explicitly re-enable Quick Edit mode on Windows. Avoid anything that requires manual mouse event capturing (`WithMouseCellMotion`) unless absolutely necessary, as it will break native copy/paste again.
