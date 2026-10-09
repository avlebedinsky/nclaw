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

const (
	keepArchives = 3
	memoryDir    = "memory"
)

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

// ArchiveSession moves the conversation history of the working directory dir aside so
// the next run starts a fresh conversation, keeping only the newest few archives. Claude's
// auto memory for dir stays in place.
func ArchiveSession(dir string) error {
	projectDir, err := sessionDir(dir)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(projectDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if err := moveHistory(projectDir, entries); err != nil {
		return err
	}
	pruneArchives(projectDir)
	return nil
}

// AutoMemoryDir returns the directory of Claude's auto memory for the working directory dir.
func AutoMemoryDir(dir string) (string, error) {
	projectDir, err := sessionDir(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(projectDir, memoryDir), nil
}

// ClearAutoMemory moves the auto memory of dir aside; the moved copy is archived with the
// conversation history by the next ArchiveSession.
func ClearAutoMemory(dir string) error {
	mem, err := AutoMemoryDir(dir)
	if err != nil {
		return err
	}
	err = os.Rename(mem, fmt.Sprintf("%s.cleared-%d", mem, time.Now().UnixNano()))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func moveHistory(projectDir string, entries []os.DirEntry) error {
	archived := fmt.Sprintf("%s.archived-%d", projectDir, time.Now().UnixNano())
	for _, e := range entries {
		if e.Name() == memoryDir {
			continue
		}
		if err := os.MkdirAll(archived, 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(projectDir, e.Name()), filepath.Join(archived, e.Name())); err != nil {
			return err
		}
	}
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
	latest, ok := latestTranscript(projectDir)
	if !ok {
		return 0, false
	}
	return latest.Size(), true
}

func latestTranscript(projectDir string) (os.FileInfo, bool) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, false
	}

	var latest os.FileInfo
	for _, e := range entries {
		info, ok := transcriptInfo(e)
		if ok && (latest == nil || info.ModTime().After(latest.ModTime())) {
			latest = info
		}
	}
	return latest, latest != nil
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
