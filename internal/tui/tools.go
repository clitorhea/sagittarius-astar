package tui

import (
	"context"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/clitorhea/sagittarius-astar.git/internal/llm"
	"github.com/clitorhea/sagittarius-astar.git/internal/sandbox"
	"github.com/clitorhea/sagittarius-astar.git/internal/workspace"
)

// ErrWriteConfirmationRequired is returned by ExecuteToolWithWrite when a tool
// produces a file mutation that needs the user's explicit approval.
var ErrWriteConfirmationRequired = fmt.Errorf("write_file: user confirmation required")

// PendingWrite holds the details of a proposed file write awaiting user confirmation.
type PendingWrite struct {
	Path    string
	Content string
	// Diff is a human-readable unified diff shown in the confirmation UI.
	// Empty for new files (no previous content to diff against).
	Diff string
}

// CommitWrite flushes a confirmed PendingWrite to disk.
func CommitWrite(pw PendingWrite) error {
	if err := os.MkdirAll(filepath.Dir(pw.Path), 0o750); err != nil {
		return fmt.Errorf("write_file: cannot create parent directory: %w", err)
	}
	if err := os.WriteFile(pw.Path, []byte(pw.Content), 0o644); err != nil {
		return fmt.Errorf("write_file: %w", err)
	}
	return nil
}

// AgentTools returns the list of tools available to the LLM agent.
func AgentTools() []llm.Tool {
	return []llm.Tool{
		// ── read_file ────────────────────────────────────────────────────────
		{
			Name:        "read_file",
			Description: "Read the contents of a file. Use start_line/end_line to read a specific range instead of the whole file when you only need part of it.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the file to read",
					},
					"start_line": map[string]any{
						"type":        "integer",
						"description": "First line to read, 1-indexed inclusive. Omit to start from the beginning.",
					},
					"end_line": map[string]any{
						"type":        "integer",
						"description": "Last line to read, 1-indexed inclusive. Omit or set to 0 to read to end of file.",
					},
				},
				"required": []string{"path"},
			},
		},
		// ── write_file ───────────────────────────────────────────────────────
		{
			Name:        "write_file",
			Description: "Write (or overwrite) a file with the given content. The user will be prompted to confirm before the write is committed. Prefer edit_file when modifying an existing file.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The file path to write to (relative or absolute)",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The full content to write to the file",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		// ── edit_file ────────────────────────────────────────────────────────
		{
			Name: "edit_file",
			Description: `Replace an exact snippet of text inside an existing file with new content.
'old_content' must match exactly once in the file — include enough surrounding lines to make it unique.
The user will see a diff preview before the change is committed.
Always prefer this over write_file when editing an existing file.`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file to edit",
					},
					"old_content": map[string]any{
						"type":        "string",
						"description": "The exact text to find and replace. Must appear exactly once in the file.",
					},
					"new_content": map[string]any{
						"type":        "string",
						"description": "The replacement text",
					},
				},
				"required": []string{"path", "old_content", "new_content"},
			},
		},
		// ── run_command ──────────────────────────────────────────────────────
		{
			Name: "run_command",
			Description: `Execute a shell command and return its stdout, stderr, and exit code.
Use for: building, testing, linting, running scripts, git operations, checking tool versions.
The user will be prompted to approve unless auto-approve mode is active (/approve-tools on).
Prefer specific, targeted commands. Avoid commands that start interactive sessions.`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The shell command to execute",
					},
					"working_dir": map[string]any{
						"type":        "string",
						"description": "Working directory for the command. Defaults to the current directory.",
					},
					"timeout_seconds": map[string]any{
						"type":        "integer",
						"description": "Max seconds to wait (1-120, default 30)",
					},
				},
				"required": []string{"command"},
			},
		},
		// ── list_directory ───────────────────────────────────────────────────
		{
			Name:        "list_directory",
			Description: "List the files and directories within a given directory (non-recursive, like ls).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The directory path to list (default: .)",
					},
				},
			},
		},
		// ── search_regex ─────────────────────────────────────────────────────
		{
			Name:        "search_regex",
			Description: "Search for a regex pattern across files in a directory (like grep -rn). Returns matching file paths, line numbers, and line content.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "The regular expression pattern to search for",
					},
					"dir": map[string]any{
						"type":        "string",
						"description": "The directory to search in (default: .)",
					},
					"file_glob": map[string]any{
						"type":        "string",
						"description": "Optional glob pattern to filter files (e.g. *.go, *.py).",
					},
				},
				"required": []string{"pattern"},
			},
		},
		// ── map_workspace ────────────────────────────────────────────────────
		{
			Name:        "map_workspace",
			Description: "Returns a tree-like map of a workspace directory. Use this to orient yourself in an unfamiliar codebase.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"dir": map[string]any{
						"type":        "string",
						"description": "The directory to map (default: .)",
					},
				},
			},
		},
		// ── web_search ───────────────────────────────────────────────────────
		{
			Name:        "web_search",
			Description: "Search the web for real-time information. Returns titles, snippets, and URLs. Follow up with web_fetch to read a full page.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The search query",
					},
				},
				"required": []string{"query"},
			},
		},
		// ── web_fetch ────────────────────────────────────────────────────────
		{
			Name:        "web_fetch",
			Description: "Fetch and extract the text content of a specific URL. Strips navigation, ads, and scripts. Code blocks are preserved. Use after web_search to read documentation or articles.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The full URL to fetch",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

// ── Tool execution ────────────────────────────────────────────────────────────

// ExecuteTool is a convenience wrapper around ExecuteToolWithWrite for tools
// that never produce a PendingWrite.
func ExecuteTool(call llm.ToolCall) (string, error) {
	result, _, err := ExecuteToolWithWrite(call)
	return result, err
}

// ExecuteToolWithWrite executes a tool call and returns its string result.
// For file-mutation tools (write_file, edit_file) it returns a PendingWrite
// that the TUI must confirm before calling CommitWrite.
// For run_command it returns ErrWriteConfirmationRequired so the TUI can gate
// execution through its approval flow; the sandbox.Options are embedded in the
// returned PendingWrite with Path="" to signal "command, not a file".
func ExecuteToolWithWrite(call llm.ToolCall) (result string, pw *PendingWrite, err error) {
	switch call.Name {

	// ── read_file ────────────────────────────────────────────────────────────
	case "read_file":
		path, ok := call.Args["path"].(string)
		if !ok {
			return "", nil, fmt.Errorf("read_file: missing or invalid 'path' argument")
		}

		const maxBytes = 50 * 1024 // 50 KB safety cap

		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil, err
		}

		// Apply line range if specified.
		startLine, hasStart := intArg(call.Args, "start_line")
		endLine, hasEnd := intArg(call.Args, "end_line")

		if hasStart || hasEnd {
			lines := strings.Split(string(data), "\n")
			total := len(lines)

			start := 1
			if hasStart && startLine >= 1 {
				start = startLine
			}
			end := total
			if hasEnd && endLine > 0 && endLine < total {
				end = endLine
			}
			if start > total {
				start = total
			}
			if end < start {
				end = start
			}

			selected := lines[start-1 : end]
			header := fmt.Sprintf("// %s  lines %d-%d (of %d)\n", path, start, end, total)
			content := header + strings.Join(selected, "\n")
			if len(content) > maxBytes {
				content = content[:maxBytes] + "\n// ... (truncated at 50 KB)"
			}
			return content, nil, nil
		}

		content := string(data)
		if len(content) > maxBytes {
			content = content[:maxBytes] + "\n// ... (truncated at 50 KB)"
		}
		return content, nil, nil

	// ── write_file ───────────────────────────────────────────────────────────
	case "write_file":
		path, ok := call.Args["path"].(string)
		if !ok || path == "" {
			return "", nil, fmt.Errorf("write_file: missing 'path' argument")
		}
		content, ok := call.Args["content"].(string)
		if !ok {
			return "", nil, fmt.Errorf("write_file: missing 'content' argument")
		}
		existing, _ := os.ReadFile(path)
		diff := simpleDiff(string(existing), content)
		return "", &PendingWrite{Path: path, Content: content, Diff: diff}, ErrWriteConfirmationRequired

	// ── edit_file ────────────────────────────────────────────────────────────
	case "edit_file":
		path, ok := call.Args["path"].(string)
		if !ok || path == "" {
			return "", nil, fmt.Errorf("edit_file: missing 'path' argument")
		}
		oldContent, ok := call.Args["old_content"].(string)
		if !ok {
			return "", nil, fmt.Errorf("edit_file: missing 'old_content' argument")
		}
		newContent, ok := call.Args["new_content"].(string)
		if !ok {
			return "", nil, fmt.Errorf("edit_file: missing 'new_content' argument")
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil, fmt.Errorf("edit_file: cannot read %s: %w", path, err)
		}
		fileStr := string(data)

		count := strings.Count(fileStr, oldContent)
		switch count {
		case 0:
			return "", nil, fmt.Errorf("edit_file: old_content not found in %s — check exact whitespace and indentation", path)
		case 1:
			// good
		default:
			return "", nil, fmt.Errorf("edit_file: old_content matches %d locations in %s — add more surrounding context to make it unique", count, path)
		}

		newFileStr := strings.Replace(fileStr, oldContent, newContent, 1)
		diff := simpleDiff(fileStr, newFileStr)
		return "", &PendingWrite{Path: path, Content: newFileStr, Diff: diff}, ErrWriteConfirmationRequired

	// ── run_command ──────────────────────────────────────────────────────────
	// run_command is gated by the TUI (auto-approve or confirmation dialog).
	// We return ErrWriteConfirmationRequired with a sentinel PendingWrite
	// (Path == "") so the TUI knows to route it through the command approval flow.
	case "run_command":
		command, ok := call.Args["command"].(string)
		if !ok || command == "" {
			return "", nil, fmt.Errorf("run_command: missing 'command' argument")
		}
		return "", &PendingWrite{Path: "", Content: command}, ErrWriteConfirmationRequired

	// ── list_directory ───────────────────────────────────────────────────────
	case "list_directory":
		dir, _ := call.Args["path"].(string)
		if dir == "" {
			dir = "."
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", nil, fmt.Errorf("list_directory: %w", err)
		}
		var lines []string
		for _, e := range entries {
			info, _ := e.Info()
			var size string
			if !e.IsDir() && info != nil {
				size = fmt.Sprintf(" (%d B)", info.Size())
			}
			suffix := ""
			if e.IsDir() {
				suffix = "/"
			}
			lines = append(lines, fmt.Sprintf("%s%s%s", e.Name(), suffix, size))
		}
		if len(lines) == 0 {
			return "(empty directory)", nil, nil
		}
		return strings.Join(lines, "\n"), nil, nil

	// ── search_regex ─────────────────────────────────────────────────────────
	case "search_regex":
		pattern, ok := call.Args["pattern"].(string)
		if !ok || pattern == "" {
			return "", nil, fmt.Errorf("search_regex: missing 'pattern' argument")
		}
		dir, _ := call.Args["dir"].(string)
		if dir == "" {
			dir = "."
		}
		fileGlob, _ := call.Args["file_glob"].(string)

		re, err := regexp.Compile(pattern)
		if err != nil {
			return "", nil, fmt.Errorf("search_regex: invalid pattern %q: %w", pattern, err)
		}

		var matches []string
		matchCount := 0
		const maxMatches = 50

		walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			if fileGlob != "" {
				matched, _ := filepath.Match(fileGlob, d.Name())
				if !matched {
					return nil
				}
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			lines := strings.Split(string(data), "\n")
			for i, line := range lines {
				if re.MatchString(line) {
					matches = append(matches, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
					matchCount++
					if matchCount >= maxMatches {
						return io.EOF
					}
				}
			}
			return nil
		})
		if walkErr != nil && walkErr != io.EOF {
			return "", nil, fmt.Errorf("search_regex: walk error: %w", walkErr)
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No matches found for pattern %q in %s", pattern, dir), nil, nil
		}
		result := strings.Join(matches, "\n")
		if matchCount >= maxMatches {
			result += fmt.Sprintf("\n... (truncated at %d matches)", maxMatches)
		}
		return result, nil, nil

	// ── map_workspace ────────────────────────────────────────────────────────
	case "map_workspace":
		dir, ok := call.Args["dir"].(string)
		if !ok || dir == "" {
			dir = "."
		}
		tree, err := workspace.MapDirectory(dir)
		if err != nil {
			return "", nil, err
		}
		return tree, nil, nil

	// ── web_search ───────────────────────────────────────────────────────────
	case "web_search":
		query, ok := call.Args["query"].(string)
		if !ok || query == "" {
			return "", nil, fmt.Errorf("web_search: missing query parameter")
		}

		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest("POST", "https://lite.duckduckgo.com/lite/",
			strings.NewReader("q="+url.QueryEscape(query)))
		if err != nil {
			return "", nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")

		resp, err := client.Do(req)
		if err != nil {
			return "", nil, err
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", nil, err
		}
		htmlBody := string(body)

		// Extract result titles.
		reTitle := regexp.MustCompile(`(?i)class=["']result-link["'][^>]*>(.*?)</a>`)
		titles := reTitle.FindAllStringSubmatch(htmlBody, 8)

		// Extract result URLs.
		reURL := regexp.MustCompile(`(?i)<a[^>]+class=["']result-link["'][^>]*href=["']([^"']+)["']`)
		urls := reURL.FindAllStringSubmatch(htmlBody, 8)

		// Extract snippets.
		reSnippet := regexp.MustCompile(`(?i)class=["']result-snippet["'][^>]*>(.*?)</td>`)
		snippets := reSnippet.FindAllStringSubmatch(htmlBody, 8)

		if len(snippets) == 0 && len(titles) == 0 {
			return "No results found for: " + query, nil, nil
		}

		stripTags := regexp.MustCompile(`<[^>]*>`)
		var results []string
		n := len(snippets)
		if len(titles) > n {
			n = len(titles)
		}
		for i := 0; i < n && i < 6; i++ {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("%d. ", i+1))

			if i < len(titles) {
				title := strings.TrimSpace(html.UnescapeString(stripTags.ReplaceAllString(titles[i][1], "")))
				if title != "" {
					sb.WriteString("**" + title + "**\n   ")
				}
			}
			if i < len(snippets) {
				snippet := strings.TrimSpace(html.UnescapeString(stripTags.ReplaceAllString(snippets[i][1], "")))
				sb.WriteString(snippet)
			}
			if i < len(urls) {
				u := strings.TrimSpace(urls[i][1])
				if u != "" {
					sb.WriteString("\n   " + u)
				}
			}
			results = append(results, sb.String())
		}
		return strings.Join(results, "\n\n"), nil, nil

	// ── web_fetch ────────────────────────────────────────────────────────────
	case "web_fetch", "web_scrape":
		targetURL, ok := call.Args["url"].(string)
		if !ok || targetURL == "" {
			return "", nil, fmt.Errorf("web_fetch: missing url parameter")
		}

		client := &http.Client{Timeout: 15 * time.Second}
		req, err := http.NewRequest("GET", targetURL, nil)
		if err != nil {
			return "", nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")

		resp, err := client.Do(req)
		if err != nil {
			return "", nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return "", nil, fmt.Errorf("web_fetch: HTTP %d for %s", resp.StatusCode, targetURL)
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", nil, err
		}
		htmlStr := string(bodyBytes)

		// Preserve code blocks before stripping tags.
		reCode := regexp.MustCompile(`(?is)<(pre|code)[^>]*>(.*?)</(pre|code)>`)
		htmlStr = reCode.ReplaceAllStringFunc(htmlStr, func(m string) string {
			inner := reCode.FindStringSubmatch(m)
			if len(inner) < 3 {
				return m
			}
			code := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(inner[2], "")
			return "\n```\n" + html.UnescapeString(code) + "\n```\n"
		})

		// Remove noisy structural sections.
		for _, tag := range []string{"script", "style", "nav", "header", "footer", "aside", "noscript"} {
			re := regexp.MustCompile(`(?is)<` + tag + `[^>]*>.*?</` + tag + `>`)
			htmlStr = re.ReplaceAllString(htmlStr, " ")
		}

		// Strip remaining HTML tags.
		htmlStr = regexp.MustCompile(`(?is)<[^>]*>`).ReplaceAllString(htmlStr, " ")

		// Decode entities and collapse whitespace.
		text := html.UnescapeString(htmlStr)
		text = regexp.MustCompile(`(?m)^\s+$`).ReplaceAllString(text, "")
		text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
		text = regexp.MustCompile(`[ \t]{2,}`).ReplaceAllString(text, " ")

		const maxBytes = 30 * 1024 // 30 KB
		if len(text) > maxBytes {
			text = text[:maxBytes] + "\n... (truncated at 30 KB)"
		}
		return strings.TrimSpace(text), nil, nil

	default:
		return "", nil, fmt.Errorf("unknown tool: %s", call.Name)
	}
}

// ExecuteRunCommand executes a run_command call directly via the sandbox.
// This is called by the TUI after user approval (or auto-approve).
// stdin may be nil (falls back to the default sudo-seed buffer via sandbox.Execute),
// or an io.Reader (e.g. an io.Pipe read-end for interactive mid-run input injection).
func ExecuteRunCommand(ctx context.Context, call llm.ToolCall, sudoPassword string, stdin io.Reader) (string, error) {
	command, _ := call.Args["command"].(string)
	workingDir, _ := call.Args["working_dir"].(string)

	var timeoutDur time.Duration
	if v, ok := call.Args["timeout_seconds"].(float64); ok && v > 0 {
		timeoutDur = time.Duration(v) * time.Second
	}

	opts := sandbox.Options{
		WorkingDir:   workingDir,
		Timeout:      timeoutDur,
		SudoPassword: sudoPassword,
	}

	var result *sandbox.Result
	var err error
	if stdin != nil {
		result, err = sandbox.ExecuteWithStdin(ctx, command, stdin, opts)
	} else {
		result, err = sandbox.Execute(ctx, command, opts)
	}
	if err != nil {
		return "", err
	}
	return result.Combined(), nil
}


// ── Internal helpers ──────────────────────────────────────────────────────────

// intArg extracts an integer from a tool args map, handling both int and float64
// (JSON numbers are always float64 after unmarshalling).
func intArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}

// simpleDiff produces a minimal line-level diff string between old and new content.
// It uses a simple approach: mark removed lines with "- " and added lines with "+ ".
func simpleDiff(oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	// Find first differing line.
	firstDiff := 0
	for firstDiff < len(oldLines) && firstDiff < len(newLines) {
		if oldLines[firstDiff] != newLines[firstDiff] {
			break
		}
		firstDiff++
	}

	// Find last differing line (from the end).
	lastOld := len(oldLines) - 1
	lastNew := len(newLines) - 1
	for lastOld > firstDiff && lastNew > firstDiff {
		if oldLines[lastOld] != newLines[lastNew] {
			break
		}
		lastOld--
		lastNew--
	}

	if firstDiff > lastOld && firstDiff > lastNew {
		return "(no changes)"
	}

	const contextLines = 2
	var sb strings.Builder

	// Context before.
	ctxStart := firstDiff - contextLines
	if ctxStart < 0 {
		ctxStart = 0
	}
	for i := ctxStart; i < firstDiff; i++ {
		sb.WriteString("  " + oldLines[i] + "\n")
	}

	// Removed lines.
	for i := firstDiff; i <= lastOld && i < len(oldLines); i++ {
		sb.WriteString("- " + oldLines[i] + "\n")
	}

	// Added lines.
	for i := firstDiff; i <= lastNew && i < len(newLines); i++ {
		sb.WriteString("+ " + newLines[i] + "\n")
	}

	// Context after.
	ctxEnd := lastNew + contextLines + 1
	if ctxEnd > len(newLines) {
		ctxEnd = len(newLines)
	}
	for i := lastNew + 1; i < ctxEnd; i++ {
		sb.WriteString("  " + newLines[i] + "\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

// toolCallResultMsg is an internal TUI message to deliver a tool execution result.
type toolCallResultMsg struct {
	CallID string
	Name   string
	Result string
	Error  error
}

// writeConfirmMsg is sent when write_file requires user confirmation before committing.
type writeConfirmMsg struct {
	CallID  string
	Name    string
	Pending PendingWrite
}
