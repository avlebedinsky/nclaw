package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), mode))
}

func bundled(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "schedule", "SKILL.md"), "bundled schedule", 0o644)
	writeFile(t, filepath.Join(src, "send-file", "SKILL.md"), "bundled send-file", 0o644)
	writeFile(t, filepath.Join(src, "send-file", "scripts", "run.sh"), "#!/bin/sh\n", 0o755)
	writeFile(t, filepath.Join(src, "README.md"), "not a skill", 0o644)
	return src
}

func TestInstall_CopiesMissingSkills(t *testing.T) {
	src, dest := bundled(t), filepath.Join(t.TempDir(), "skills")

	installed, err := Install(src, dest)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"schedule", "send-file"}, installed)
	data, err := os.ReadFile(filepath.Join(dest, "schedule", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "bundled schedule", string(data))
	info, err := os.Stat(filepath.Join(dest, "send-file", "scripts", "run.sh"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o111, "executable bit must survive")
	assert.NoFileExists(t, filepath.Join(dest, "README.md"))

	leftovers, err := filepath.Glob(filepath.Join(dest, ".*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

func TestInstall_KeepsCustomizedSkill(t *testing.T) {
	src, dest := bundled(t), t.TempDir()
	writeFile(t, filepath.Join(dest, "schedule", "SKILL.md"), "my timezone tweaks", 0o644)

	installed, err := Install(src, dest)

	require.NoError(t, err)
	assert.Equal(t, []string{"send-file"}, installed)
	data, err := os.ReadFile(filepath.Join(dest, "schedule", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "my timezone tweaks", string(data))
}

func TestInstall_SkipsDanglingLink(t *testing.T) {
	src, dest := bundled(t), t.TempDir()
	require.NoError(t, os.Symlink("../../.agents/skills/schedule", filepath.Join(dest, "schedule")))

	installed, err := Install(src, dest)

	require.NoError(t, err)
	assert.Equal(t, []string{"send-file"}, installed)
}

func TestInstall_MissingSource(t *testing.T) {
	installed, err := Install(filepath.Join(t.TempDir(), "missing"), t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, installed)
}

func TestInstall_IsIdempotent(t *testing.T) {
	src, dest := bundled(t), t.TempDir()
	_, err := Install(src, dest)
	require.NoError(t, err)

	installed, err := Install(src, dest)

	require.NoError(t, err)
	assert.Empty(t, installed)
}

func TestDirs(t *testing.T) {
	assert.Equal(t, []string{"/c/skills"}, Dirs("claude", "/c", "/h"))
	assert.Equal(t, []string{"/c/skills"}, Dirs("copilot", "/c", "/h"))
	assert.Equal(t, []string{"/c/skills", "/h/.codex/skills"}, Dirs("codex", "/c", "/h"))
	assert.Equal(t, []string{"/c/skills", "/h/.gemini/skills"}, Dirs("gemini", "/c", "/h"))
}
