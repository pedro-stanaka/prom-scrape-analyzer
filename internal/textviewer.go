package internal

import (
	"os"
	"path/filepath"
)

// CreateTempFileWithContent creates a temporary file with the given content and returns its path.
// The caller is responsible for removing the file when done.
func CreateTempFileWithContent(content string) string {
	tmpfile, err := os.CreateTemp("", "prom-scrape-analyzer-*.txt")
	if err != nil {
		return ""
	}

	if _, err := tmpfile.WriteString(content); err != nil {
		tmpfile.Close()
		os.Remove(tmpfile.Name())
		return ""
	}

	if err := tmpfile.Sync(); err != nil {
		tmpfile.Close()
		os.Remove(tmpfile.Name())
		return ""
	}

	if err := tmpfile.Close(); err != nil {
		os.Remove(tmpfile.Name())
		return ""
	}

	return filepath.Clean(tmpfile.Name())
}
