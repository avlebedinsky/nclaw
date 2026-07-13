package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// claudeProjectSlug converts an absolute working directory into Claude
// Code's project directory naming convention: every "/" becomes a "-"
// (e.g. "/app/data/-100/1224" -> "-app-data--100-1224").
func claudeProjectSlug(dir string) string {
	return strings.ReplaceAll(dir, "/", "-")
}

// claudeProjectsDir returns the base directory where Claude Code stores
// per-project session transcripts.
func claudeProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// claudeSessionSize returns the size in bytes of the most recently modified
// session transcript (.jsonl) for the Claude Code project backing dir, and
// whether a transcript was found at all.
func claudeSessionSize(projectsDir, dir string) (int64, bool) {
	entries, err := os.ReadDir(filepath.Join(projectsDir, claudeProjectSlug(dir)))
	if err != nil {
		return 0, false
	}

	var latestSize int64
	var latestMod time.Time
	found := false

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		if !found || info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestSize = info.Size()
			found = true
		}
	}

	return latestSize, found
}

// resetClaudeSession archives the Claude Code session directory backing dir
// so the next invocation starts with a clean context instead of continuing
// an oversized conversation. It is a no-op if no session exists yet.
func resetClaudeSession(projectsDir, dir string) error {
	projectDir := filepath.Join(projectsDir, claudeProjectSlug(dir))

	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		return nil
	}

	archived := fmt.Sprintf("%s.archived-%d", projectDir, time.Now().Unix())
	return os.Rename(projectDir, archived)
}
