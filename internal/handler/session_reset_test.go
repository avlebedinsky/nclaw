package handler

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeProjectSlug(t *testing.T) {
	slug := claudeProjectSlug("/app/data/-1003874128370/1224")
	assert.Equal(t, "-app-data--1003874128370-1224", slug)
}

func TestClaudeSessionSize_NoProjectDir(t *testing.T) {
	size, ok := claudeSessionSize(t.TempDir(), "/app/data/123")
	assert.False(t, ok)
	assert.Zero(t, size)
}

func TestClaudeSessionSize_IgnoresNonJSONL(t *testing.T) {
	projectsDir := t.TempDir()
	dir := "/app/data/123"
	projectDir := filepath.Join(projectsDir, claudeProjectSlug(dir))
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "memory"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "notes.txt"), []byte("ignored"), 0o644))

	size, ok := claudeSessionSize(projectsDir, dir)
	assert.False(t, ok)
	assert.Zero(t, size)
}

func TestClaudeSessionSize_PicksMostRecentTranscript(t *testing.T) {
	projectsDir := t.TempDir()
	dir := "/app/data/123"
	projectDir := filepath.Join(projectsDir, claudeProjectSlug(dir))
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	older := filepath.Join(projectDir, "older.jsonl")
	require.NoError(t, os.WriteFile(older, []byte("111"), 0o644))
	require.NoError(t, os.Chtimes(older, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)))

	newer := filepath.Join(projectDir, "newer.jsonl")
	require.NoError(t, os.WriteFile(newer, []byte("22222"), 0o644))

	size, ok := claudeSessionSize(projectsDir, dir)
	require.True(t, ok)
	assert.Equal(t, int64(5), size)
}

func TestResetClaudeSession_ArchivesProjectDir(t *testing.T) {
	projectsDir := t.TempDir()
	dir := "/app/data/123"
	projectDir := filepath.Join(projectsDir, claudeProjectSlug(dir))
	require.NoError(t, os.MkdirAll(projectDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte("data"), 0o644))

	require.NoError(t, resetClaudeSession(projectsDir, dir))

	_, err := os.Stat(projectDir)
	assert.True(t, os.IsNotExist(err))

	matches, err := filepath.Glob(projectDir + ".archived-*")
	require.NoError(t, err)
	assert.Len(t, matches, 1)
}

func TestResetClaudeSession_NoOpWhenMissing(t *testing.T) {
	projectsDir := t.TempDir()
	assert.NoError(t, resetClaudeSession(projectsDir, "/app/data/does-not-exist"))
}
