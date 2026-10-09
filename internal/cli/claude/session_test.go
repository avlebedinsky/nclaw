package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectSlug(t *testing.T) {
	cases := map[string]string{
		"/app/data/-1003874128370/1224":               "-app-data--1003874128370-1224",
		"/home/lav/projects/fansee-wall/.claude/work": "-home-lav-projects-fansee-wall--claude-work",
		"/data/my_bot/1":                              "-data-my-bot-1",
		"/data/чат":                                   "-data----",
		"/data/😀":                                     "-data---",
	}
	for in, want := range cases {
		assert.Equal(t, want, projectSlug(in), in)
	}
}

func TestConfigDir_HonorsEnv(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom/claude")
	dir, err := ConfigDir()
	require.NoError(t, err)
	assert.Equal(t, "/custom/claude", dir)
}

func TestConfigDir_DefaultsToHome(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "/home/someone")
	dir, err := ConfigDir()
	require.NoError(t, err)
	assert.Equal(t, "/home/someone/.claude", dir)
}

func setupProject(t *testing.T) (workDir, projectDir string) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	workDir = filepath.Join(t.TempDir(), "my_bot", "123")
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	resolved, err := filepath.EvalSymlinks(workDir)
	require.NoError(t, err)
	projectDir = filepath.Join(configDir, "projects", projectSlug(resolved))
	require.NoError(t, os.MkdirAll(projectDir, 0o755))
	return workDir, projectDir
}

func TestSessionSize_NoProjectDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	size, ok := SessionSize(t.TempDir())
	assert.False(t, ok)
	assert.Zero(t, size)
}

func TestSessionSize_IgnoresNonJSONL(t *testing.T) {
	workDir, projectDir := setupProject(t)
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "memory"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "notes.txt"), []byte("ignored"), 0o644))

	size, ok := SessionSize(workDir)
	assert.False(t, ok)
	assert.Zero(t, size)
}

func TestSessionSize_PicksMostRecentTranscript(t *testing.T) {
	workDir, projectDir := setupProject(t)
	older := filepath.Join(projectDir, "older.jsonl")
	require.NoError(t, os.WriteFile(older, []byte("111"), 0o644))
	require.NoError(t, os.Chtimes(older, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "newer.jsonl"), []byte("22222"), 0o644))

	size, ok := SessionSize(workDir)
	require.True(t, ok)
	assert.Equal(t, int64(5), size)
}

func TestSessionSize_RelativeDir(t *testing.T) {
	workDir, projectDir := setupProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "s.jsonl"), []byte("1234"), 0o644))
	t.Chdir(filepath.Dir(workDir))

	size, ok := SessionSize(filepath.Base(workDir))
	require.True(t, ok)
	assert.Equal(t, int64(4), size)
}

func TestArchiveSession_MovesHistoryAndKeepsMemory(t *testing.T) {
	workDir, projectDir := setupProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte("data"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "session", "subagents"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "memory"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "memory", "MEMORY.md"), []byte("- fact"), 0o644))

	require.NoError(t, ArchiveSession(workDir))

	matches, err := filepath.Glob(projectDir + ".archived-*")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.FileExists(t, filepath.Join(matches[0], "session.jsonl"))
	assert.DirExists(t, filepath.Join(matches[0], "session", "subagents"))
	assert.NoDirExists(t, filepath.Join(matches[0], "memory"))

	memory, err := os.ReadFile(filepath.Join(projectDir, "memory", "MEMORY.md"))
	require.NoError(t, err)
	assert.Equal(t, "- fact", string(memory))
	_, found := SessionSize(workDir)
	assert.False(t, found)
}

func TestArchiveSession_OnlyMemoryCreatesNoArchive(t *testing.T) {
	workDir, projectDir := setupProject(t)
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "memory"), 0o755))

	require.NoError(t, ArchiveSession(workDir))

	matches, err := filepath.Glob(projectDir + ".archived-*")
	require.NoError(t, err)
	assert.Empty(t, matches)
	assert.DirExists(t, filepath.Join(projectDir, "memory"))
}

func TestArchiveSession_NoOpWhenMissing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	assert.NoError(t, ArchiveSession(t.TempDir()))
}

func TestArchiveSession_KeepsNewestArchives(t *testing.T) {
	workDir, projectDir := setupProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte("data"), 0o644))
	for _, stamp := range []int64{1700000001, 1700000002, 1700000003, 1700000004} {
		require.NoError(t, os.MkdirAll(fmt.Sprintf("%s.archived-%d", projectDir, stamp), 0o755))
	}

	require.NoError(t, ArchiveSession(workDir))

	matches, err := filepath.Glob(projectDir + ".archived-*")
	require.NoError(t, err)
	require.Len(t, matches, keepArchives)
	assert.NotContains(t, matches, projectDir+".archived-1700000001")
	assert.NotContains(t, matches, projectDir+".archived-1700000002")
	assert.Contains(t, matches, projectDir+".archived-1700000004")
}

func TestClearAutoMemory_MovesTheMemoryAside(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	work := t.TempDir()
	mem, err := AutoMemoryDir(work)
	require.NoError(t, err)
	assert.Equal(t, "memory", filepath.Base(mem))
	require.NoError(t, ClearAutoMemory(work))
	require.NoError(t, os.MkdirAll(mem, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("- fact"), 0o644))

	require.NoError(t, ClearAutoMemory(work))

	assert.NoDirExists(t, mem)
	moved, err := filepath.Glob(mem + ".cleared-*")
	require.NoError(t, err)
	require.Len(t, moved, 1)
	assert.FileExists(t, filepath.Join(moved[0], "MEMORY.md"))
}
