package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MapDirectory returns a tree-like string representation of the given directory.
// It skips common ignored directories like .git, node_modules, vendor, etc.
func MapDirectory(root string) (string, error) {
	var builder strings.Builder

	// Simple heuristic for ignoring files/dirs.
	// In a full implementation, we'd parse .gitignore.
	ignoredNames := map[string]bool{
		".git":         true,
		"node_modules": true,
		"vendor":       true,
		"bin":          true,
		".gocache":     true,
		"dist":         true,
		"build":        true,
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if ignoredNames[d.Name()] {
				return filepath.SkipDir
			}
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if relPath == "." {
			builder.WriteString(fmt.Sprintf("%s/\n", filepath.Base(root)))
			return nil
		}

		depth := strings.Count(relPath, string(os.PathSeparator))
		indent := strings.Repeat("  ", depth)

		name := d.Name()
		if d.IsDir() {
			name += "/"
		}

		builder.WriteString(fmt.Sprintf("%s%s\n", indent, name))
		return nil
	})

	if err != nil {
		return "", err
	}

	return builder.String(), nil
}
