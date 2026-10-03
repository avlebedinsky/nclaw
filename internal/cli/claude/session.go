package claude

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const keepArchives = 3

// ConfigDir returns Claude Code's configuration directory: $CLAUDE_CONFIG_DIR, or ~/.claude when unset.
func ConfigDir() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

// SessionSize returns the size in bytes of the most recently modified session
// transcript for the working directory dir, and whether one exists.
func SessionSize(dir string) (int64, bool) {
	projectDir, err := sessionDir(dir)
	if err != nil {
		return 0, false
	}
	return latestTranscriptSize(projectDir)
}

// ArchiveSession moves the session history of the working directory dir aside so the
// next run starts a fresh conversation, keeping only the newest few archives.
func ArchiveSession(dir string) error {
	projectDir, err := sessionDir(dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		return nil
	}

	archived := fmt.Sprintf("%s.archived-%d", projectDir, time.Now().UnixNano())
	if err := os.Rename(projectDir, archived); err != nil {
		return err
	}
	pruneArchives(projectDir)
	return nil
}

func sessionDir(dir string) (string, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Join(configDir, "projects", projectSlug(abs)), nil
}

func projectSlug(dir string) string {
	var b strings.Builder
	for _, r := range dir {
		switch {
		case isASCIIAlnum(r):
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func isASCIIAlnum(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func latestTranscriptSize(projectDir string) (int64, bool) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return 0, false
	}

	var latest os.FileInfo
	for _, e := range entries {
		info, ok := transcriptInfo(e)
		if ok && (latest == nil || info.ModTime().After(latest.ModTime())) {
			latest = info
		}
	}

	if latest == nil {
		return 0, false
	}
	return latest.Size(), true
}

func transcriptInfo(e os.DirEntry) (os.FileInfo, bool) {
	if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
		return nil, false
	}
	info, err := e.Info()
	return info, err == nil
}

func pruneArchives(projectDir string) {
	archives, err := filepath.Glob(projectDir + ".archived-*")
	if err != nil || len(archives) <= keepArchives {
		return
	}
	sort.Slice(archives, func(i, j int) bool { return archiveStamp(archives[i]) < archiveStamp(archives[j]) })
	for _, old := range archives[:len(archives)-keepArchives] {
		if err := os.RemoveAll(old); err != nil {
			log.Printf("claude: remove old session archive %s: %v", old, err)
		}
	}
}

func archiveStamp(path string) int64 {
	stamp := path[strings.LastIndex(path, ".archived-")+len(".archived-"):]
	n, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
