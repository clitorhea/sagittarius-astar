# 🚀 Sagittarius A* (`aig`) — Future Roadmap

The current version of `aig` successfully establishes the core foundation: a non-blocking streaming TUI, multi-provider support, and basic sandboxed execution. 

To evolve `aig` from a "terminal chatbot" into a **fully-fledged autonomous terminal agent**, here is a recommended roadmap of features and functionalities.

---

## Phase 1: Persistence & Configuration 💾
*Currently, context is lost when the app closes, and configuration is limited to flags/env vars.*

*   **Session Management (SQLite / JSON)**
    *   Persist conversation history to disk (`~/.local/share/aig/sessions.db`).
    *   **New Commands:** `/history` (view past chats), `/save <name>`, `/load <id>`.
*   **Centralized Configuration File**
    *   Introduce `~/.config/aig/config.yaml` to store API keys, default models, and theme preferences.
*   **System Prompt Personas**
    *   Allow users to define custom personas in the config (e.g., `sysadmin`, `code-reviewer`, `creative-writer`).
    *   Usage: `aig --persona sysadmin`.

## Phase 2: Context Awareness & RAG 🧠
*The agent currently only knows what you type into the prompt.*

*   **File Context Injection**
    *   Allow users to attach files directly from the prompt: `explain /read(main.go) and /read(Makefile)`.
    *   Drag-and-drop file support (Bubble Tea supports terminal file drops).
*   **Workspace Mapping**
    *   A command like `/map .` that automatically reads `.gitignore`, maps the directory tree structure, and feeds it to the system prompt so the AI understands the surrounding codebase.
*   **Context Window Management**
    *   Implement automatic token counting.
    *   When nearing the model's context limit, automatically summarize older messages to maintain long-running sessions without crashing.

## Phase 3: True Agentic Tool Calling 🛠️
*Currently, execution is limited to parsing markdown code blocks. We should upgrade to native LLM function calling.*

*   **Native Tool Integration**
    *   Implement the native function-calling APIs for Gemini and DeepSeek.
    *   The model decides *when* to call a tool, rather than relying on markdown regex parsing.
*   **Standard Tool Library**
    *   `read_file`, `write_file`, `list_directory`, `search_regex`.
    *   `web_search` (e.g., via DuckDuckGo API) to give the agent internet access.
*   **Multi-Step Autonomous Execution**
    *   Allow the user to grant "auto-run" permissions for read-only tools, while still prompting for write/execute tools.

## Phase 4: TUI & UX Polish ✨
*Taking the Catppuccin Bubble Tea interface to the next level.*

*   **Interactive Command Modal**
    *   Instead of just `[Execute this bash block? (y/N)]` inline, open a centered floating modal that allows the user to **edit** the command before running it.
*   **Tabs / Multi-Chat Interface**
    *   Add a sidebar or top tab bar to switch between multiple active conversations without restarting the app.
*   **Dynamic Theming**
    *   Allow users to swap color palettes (Catppuccin, Nord, Dracula, Light modes) via the config file.
*   **Help & Keybind Modal**
    *   Press `?` to open an overlay showing all available keyboard shortcuts and slash commands.

---

### 🗣️ Which path sounds best?

Are you more interested in leaning into the **Agentic / Tool Calling** side first (Phase 3), or stabilizing the daily-driver experience with **Persistence and Configs** (Phase 1)? Let me know what you think of this roadmap!
