// yib/utils/utils.go
package utils

import (
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
)

var BackupDir string

// BtoI converts a boolean to an integer (1 for true, 0 for false).
func BtoI(b bool) int {
	if b {
		return 1
	}
	return 0
}

// CreatePlaceholderImage generates a default placeholder image if one doesn't exist.
func CreatePlaceholderImage(logger *slog.Logger) {
	placeholderPath := filepath.Join("static", "placeholder.png")
	if _, err := os.Stat(placeholderPath); err == nil {
		return // File already exists
	}

	const placeholderBase64 = `iVBORw0KGgoAAAANSUhEUgAAAJYAAACWCAYAAAA8AXHiAAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAARJSURBVHhe7d3NbhoxEIXh7zcr8QZ6g55gT3AbR2AnKJHQaPIVO3fBIHkyE/M306FnoEmqgP5fT4vL6k+l6vW373O/X1f/vT4v3/f9+n79uQCAAQMGDBgwYMGAAQMGDBgwYMCAAAECAwYMGDBgwIABAwYMGDBgwIABAgQGDBgwYMGAAQMGDBgwYMCAAAECI4zL9Y/XFwsGDBgwYMCAAAECI4zD1Wf5pL5YMGDAgAEDBggQGGFsvV7Xn8tfrj8/LhYMGLC/+Z/f337P/v7xMez3LzaMgwEDBgwYMGDAgAEDBgwYMGDAgAABAgMGDBgwYMGAAQMGDBgwYMCAAAECAwYMGDBgwIABAwYMGDBgwIABAgQIjDAu1x+vt2DAgAEDBgwQIBCy5Xp9P9Of2zZgwIABAwYMECCw5fo5v1gwYMCgGRi3f/v7PzDuv/Z/Zwx7v1gwYMCgGRiX68/z+mV+Wf3ZMgYMGDBgwIABAwQIjDAu159yBgwYMGDAgAABAse1/pL1ZcKAAQMGDBgwYMCAAAECI4zL9U/29YIBg/8y7L/x+h+3/yOswYABAwYMGDBgwIABAwYMGDBgwIABAgQGDBgwYMGAAQMGDBgwYMCAAAECBAYMGDBgwIABAwYMGDBgwIABAgQIjDAu15/l/bJgwIABA/9l+J9bNxgzYMCgGRiX65/s5/Jl3aB9/zPs/y5rx4ABAwYMGDBgwIABAwYMGDBgwIABAgQGDBgwYMGAAQMGDBgwYMCAAAECBAYMGDBgwIABAwYMGDBgwIABAgQIjDAu15/l9YpBwwYMGDCowLj8v64f5f2yYMCAAYO6gXHZf53+3LpBM2DAgEEdwLhc/7Jg0IABAwYMGDBgQICgXVZ/+t/v8/pX+bJg0IABAwYMGDCowbn8/+t7+bP+smHQgEEdwLjcf7L+kmHQgEEdwLj8v+v35cu6QTNg0IBBHcC4XP+yYNCgAYO6gXHZf51+vK4bNCDQgEEdwLj8//V6Xb9c/0m6QdCAAQMGDBgwYMAAAQIjDMv15/l9WTAgQGCE8frj8n8OGBCowLhcf7L+EmFAQICgXVZ/+t/vAwYEijAu1/+sf1kwIEBgYDy+X/+v/3+zYECgAcOy/pL1ZcKAAIECI4zD1Wd5WTAgQGCEsXV5XV/Wf7JgQIABA9v1+r+v/3/dYMDAgAEDBgwYMGDAgAEDBgwYMGDAgAABAgMGDBgwYMGAAQMGDBgwYMCAAAECAwYMGDBgwIABAwYMGDBgwIABAgQGDBgwYMGAAQMGDBgwYMCAAAECI4zL9Wd5vVgwAAAAAElFTkSuQmCC`
	data, err := base64.StdEncoding.DecodeString(placeholderBase64)
	if err != nil {
		logger.Error("Error decoding placeholder image", "error", err)
		return
	}
	os.MkdirAll("./static", 0755)
	err = os.WriteFile(placeholderPath, data, 0666)
	if err != nil {
		logger.Error("Error writing placeholder image", "error", err)
	} else {
		logger.Info("Created missing placeholder.png in static/ directory.")
	}
}

// ReadBanner reads the global banner content from its file.
func ReadBanner(bannerFile string) (string, error) {
	if _, err := os.Stat(bannerFile); os.IsNotExist(err) {
		return "", nil // File doesn't exist, not an error
	}
	content, err := os.ReadFile(bannerFile)
	if err != nil {
		// Can't use logger here as it's not available in all contexts
		// log.Printf("ERROR: Could not read banner file %s: %v", bannerFile, err)
		return "", err
	}
	return string(content), nil
}

// WriteBanner writes content to the global banner file.
func WriteBanner(bannerFile, content string) error {
	// If content is empty, delete the file to hide the banner.
	if content == "" {
		if _, err := os.Stat(bannerFile); err == nil {
			return os.Remove(bannerFile)
		}
		return nil
	}
	err := os.WriteFile(bannerFile, []byte(content), 0644)
	if err != nil {
		// log.Printf("ERROR: Could not write to banner file %s: %v", bannerFile, err)
	}
	return err
}
