package tui

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/clitorhea/sagittarius-astar.git/internal/llm"
	"github.com/clitorhea/sagittarius-astar.git/internal/workspace"
)

// AgentTools returns the list of tools available to the LLM agent.
func AgentTools() []llm.Tool {
	return []llm.Tool{
		{
			Name:        "read_file",
			Description: "Read the contents of a file in the workspace.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the file to read",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "map_workspace",
			Description: "Returns a tree-like map of the workspace directory.",
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
		{
			Name:        "web_search",
			Description: "Search the web for real-time information, news, or weather.",
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
		{
			Name:        "web_scrape",
			Description: "Scrape the text content of a specific URL.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The full URL to scrape",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

// ExecuteTool safely executes a requested tool call locally and returns its string result.
func ExecuteTool(call llm.ToolCall) (string, error) {
	switch call.Name {
	case "read_file":
		path, ok := call.Args["path"].(string)
		if !ok {
			return "", fmt.Errorf("missing or invalid 'path' argument")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil

	case "map_workspace":
		dir, ok := call.Args["dir"].(string)
		if !ok || dir == "" {
			dir = "."
		}
		tree, err := workspace.MapDirectory(dir)
		if err != nil {
			return "", err
		}
		return tree, nil

	case "web_search":
		query, ok := call.Args["query"].(string)
		if !ok || query == "" {
			return "", fmt.Errorf("missing query parameter")
		}

		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest("POST", "https://lite.duckduckgo.com/lite/", strings.NewReader("q="+url.QueryEscape(query)))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		html := string(body)

		// Naive extraction: find <td class='result-snippet'>...</td>
		re := regexp.MustCompile(`(?i)class="result-snippet"[^>]*>(.*?)</td>`)
		matches := re.FindAllStringSubmatch(html, 5)

		if len(matches) == 0 {
			return "No results found for query.", nil
		}

		var results []string
		for i, m := range matches {
			// strip HTML tags safely
			text := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(m[1], "")
			results = append(results, fmt.Sprintf("%d. %s", i+1, strings.TrimSpace(text)))
		}

		return strings.Join(results, "\n"), nil

	case "web_scrape":
		targetURL, ok := call.Args["url"].(string)
		if !ok || targetURL == "" {
			return "", fmt.Errorf("missing url parameter")
		}

		client := &http.Client{Timeout: 15 * time.Second}
		req, err := http.NewRequest("GET", targetURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return "", fmt.Errorf("failed to fetch, status code: %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		htmlStr := string(body)

		// Remove <script> and <style> tags and their contents
		reScript := regexp.MustCompile(`(?is)<script.*?>.*?</script>`)
		htmlStr = reScript.ReplaceAllString(htmlStr, " ")

		reStyle := regexp.MustCompile(`(?is)<style.*?>.*?</style>`)
		htmlStr = reStyle.ReplaceAllString(htmlStr, " ")

		// Remove all other HTML tags
		reTags := regexp.MustCompile(`(?is)<[^>]*>`)
		htmlStr = reTags.ReplaceAllString(htmlStr, " ")

		// Decode HTML entities and clean up whitespace
		text := html.UnescapeString(htmlStr)
		reWhitespace := regexp.MustCompile(`(?m)^\s+$`)
		text = reWhitespace.ReplaceAllString(text, "")
		reMultipleNewlines := regexp.MustCompile(`\n{3,}`)
		text = reMultipleNewlines.ReplaceAllString(text, "\n\n")

		// Cap size to avoid overwhelming the context window (~20,000 chars is safe)
		if len(text) > 20000 {
			text = text[:20000] + "\n... (truncated)"
		}

		return strings.TrimSpace(text), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", call.Name)
	}
}

// toolCallResultMsg is an internal TUI message to deliver a tool execution result.
type toolCallResultMsg struct {
	CallID string
	Name   string
	Result string
	Error  error
}
