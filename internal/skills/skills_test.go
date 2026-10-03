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

func stateFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "data", ".nclaw-skills.json")
}

func readSchedule(t *testing.T, dest string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dest, "schedule", "SKILL.md"))
	require.NoError(t, err)
	return string(data)
}

func TestInstall_CopiesMissingSkills(t *testing.T) {
	src, dest := bundled(t), filepath.Join(t.TempDir(), "skills")

	rep, err := Install(src, dest, stateFile(t))

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"schedule", "send-file"}, rep.Installed)
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

	rep, err := Install(src, dest, stateFile(t))

	require.NoError(t, err)
	assert.Equal(t, []string{"send-file"}, rep.Installed)
	assert.Equal(t, []string{"schedule"}, rep.Kept)
	assert.Equal(t, "my timezone tweaks", readSchedule(t, dest))
}

func TestInstall_SkipsDanglingLink(t *testing.T) {
	src, dest := bundled(t), t.TempDir()
	require.NoError(t, os.Symlink("../../.agents/skills/schedule", filepath.Join(dest, "schedule")))

	rep, err := Install(src, dest, stateFile(t))

	require.NoError(t, err)
	assert.Equal(t, []string{"send-file"}, rep.Installed)
	assert.Equal(t, []string{"schedule"}, rep.Kept)
}

func TestInstall_MissingSource(t *testing.T) {
	rep, err := Install(filepath.Join(t.TempDir(), "missing"), t.TempDir(), stateFile(t))
	require.NoError(t, err)
	assert.Empty(t, rep)
}

func TestInstall_IsIdempotent(t *testing.T) {
	src, dest, st := bundled(t), t.TempDir(), stateFile(t)
	_, err := Install(src, dest, st)
	require.NoError(t, err)

	rep, err := Install(src, dest, st)

	require.NoError(t, err)
	assert.Empty(t, rep)
}

func TestInstall_UpdatesUntouchedCopy(t *testing.T) {
	src, dest, st := bundled(t), t.TempDir(), stateFile(t)
	_, err := Install(src, dest, st)
	require.NoError(t, err)
	writeFile(t, filepath.Join(src, "schedule", "SKILL.md"), "schedule v2", 0o644)
	writeFile(t, filepath.Join(src, "schedule", "examples.md"), "new file", 0o644)

	rep, err := Install(src, dest, st)

	require.NoError(t, err)
	assert.Equal(t, []string{"schedule"}, rep.Updated)
	assert.Equal(t, "schedule v2", readSchedule(t, dest))
	assert.FileExists(t, filepath.Join(dest, "schedule", "examples.md"))
	leftovers, err := filepath.Glob(filepath.Join(dest, ".*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

func TestInstall_KeepsCopyChangedAfterInstall(t *testing.T) {
	src, dest, st := bundled(t), t.TempDir(), stateFile(t)
	_, err := Install(src, dest, st)
	require.NoError(t, err)
	writeFile(t, filepath.Join(dest, "schedule", "SKILL.md"), "edited by hand", 0o644)
	writeFile(t, filepath.Join(src, "schedule", "SKILL.md"), "schedule v2", 0o644)

	rep, err := Install(src, dest, st)

	require.NoError(t, err)
	assert.Equal(t, []string{"schedule"}, rep.Kept)
	assert.Empty(t, rep.Updated)
	assert.Equal(t, "edited by hand", readSchedule(t, dest))
}

func TestInstall_AdoptsIdenticalSkillAndUpdatesItLater(t *testing.T) {
	src, dest, st := bundled(t), t.TempDir(), stateFile(t)
	writeFile(t, filepath.Join(dest, "schedule", "SKILL.md"), "bundled schedule", 0o644)

	rep, err := Install(src, dest, st)
	require.NoError(t, err)
	assert.Equal(t, []string{"send-file"}, rep.Installed)
	assert.Empty(t, rep.Kept)

	writeFile(t, filepath.Join(src, "schedule", "SKILL.md"), "schedule v2", 0o644)
	rep, err = Install(src, dest, st)

	require.NoError(t, err)
	assert.Equal(t, []string{"schedule"}, rep.Updated)
	assert.Equal(t, "schedule v2", readSchedule(t, dest))
}

func TestInstall_NeverReplacesLinkedSkill(t *testing.T) {
	src, dest, st := bundled(t), t.TempDir(), stateFile(t)
	shared := filepath.Join(t.TempDir(), "schedule")
	writeFile(t, filepath.Join(shared, "SKILL.md"), "bundled schedule", 0o644)
	require.NoError(t, os.Symlink(shared, filepath.Join(dest, "schedule")))
	writeFile(t, filepath.Join(src, "schedule", "SKILL.md"), "schedule v2", 0o644)

	rep, err := Install(src, dest, st)

	require.NoError(t, err)
	assert.Equal(t, []string{"schedule"}, rep.Kept)
	info, err := os.Lstat(filepath.Join(dest, "schedule"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
}

func TestInstall_TracksEachDestinationSeparately(t *testing.T) {
	src, st := bundled(t), stateFile(t)
	first, second := t.TempDir(), t.TempDir()
	_, err := Install(src, first, st)
	require.NoError(t, err)
	writeFile(t, filepath.Join(second, "schedule", "SKILL.md"), "someone else's", 0o644)

	rep, err := Install(src, second, st)

	require.NoError(t, err)
	assert.Equal(t, []string{"schedule"}, rep.Kept)
	assert.Equal(t, "someone else's", readSchedule(t, second))
}

func TestInstall_UnreadableStateStartsOver(t *testing.T) {
	src, dest, st := bundled(t), t.TempDir(), stateFile(t)
	writeFile(t, st, "{not json", 0o644)

	rep, err := Install(src, dest, st)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"schedule", "send-file"}, rep.Installed)
	data, err := os.ReadFile(st)
	require.NoError(t, err)
	assert.Contains(t, string(data), "send-file")
}

func TestDirs(t *testing.T) {
	assert.Equal(t, []string{"/c/skills"}, Dirs("claude", "/c", "/h"))
	assert.Equal(t, []string{"/c/skills"}, Dirs("copilot", "/c", "/h"))
	assert.Equal(t, []string{"/c/skills", "/h/.codex/skills"}, Dirs("codex", "/c", "/h"))
	assert.Equal(t, []string{"/c/skills", "/h/.gemini/skills"}, Dirs("gemini", "/c", "/h"))
}
