// Package session manages local persistence of conversation history.
//
// Each session is stored as a JSON file in the user's data directory:
//
//	Linux/macOS : ~/.local/share/aig/sessions/
//	Windows     : %LocalAppData%/aig/sessions/
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/clitorhea/sagittarius-astar.git/internal/llm"
)

// Session represents a single saved conversation thread.
type Session struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Provider  string        `json:"provider"`
	Model     string        `json:"model"`
	History   []llm.Message `json:"history"`
}

// Dir returns the platform-appropriate directory for sessions.
func Dir() string {
	// Standard XDG compliance
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback to cache directory
		cache, _ := os.UserCacheDir()
		return filepath.Join(cache, "aig", "sessions")
	}

	if runtime.GOOS == "windows" {
		localApp := os.Getenv("LocalAppData")
		if localApp != "" {
			return filepath.Join(localApp, "aig", "sessions")
		}
		return filepath.Join(home, "AppData", "Local", "aig", "sessions")
	}

	// Linux / macOS standard path
	return filepath.Join(home, ".local", "share", "aig", "sessions")
}

// GenerateID produces a unique timestamp-based session identifier.
func GenerateID() string {
	return time.Now().Format("20060102-150405")
}

// Save writes the session to disk.
func Save(s *Session) error {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("session: cannot create directory: %w", err)
	}

	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("session: failed to marshal: %w", err)
	}

	filename := filepath.Join(dir, s.ID+".json")
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		return fmt.Errorf("session: failed to write file: %w", err)
	}

	return nil
}

// Load reads a session by its ID.
func Load(id string) (*Session, error) {
	// Clean the ID of any directory traversal characters
	id = filepath.Base(id)
	id = strings.TrimSuffix(id, ".json")

	filename := filepath.Join(Dir(), id+".json")
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("session: failed to read %s: %w", id, err)
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("session: failed to parse %s: %w", id, err)
	}

	return &s, nil
}

// List returns all saved sessions, sorted from newest to oldest (by UpdatedAt).
func List() ([]Session, error) {
	dir := Dir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session: failed to list directory: %w", err)
	}

	var sessions []Session
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".json")
		s, err := Load(id)
		if err != nil {
			// Skip corrupt session files rather than failing the whole list
			continue
		}
		sessions = append(sessions, *s)
	}

	// Sort newest first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

// Delete removes a session file.
func Delete(id string) error {
	id = filepath.Base(id)
	id = strings.TrimSuffix(id, ".json")
	filename := filepath.Join(Dir(), id+".json")

	if err := os.Remove(filename); err != nil {
		return fmt.Errorf("session: failed to delete %s: %w", id, err)
	}
	return nil
}
