package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSkillFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte(content), 0o644))
}

func TestList_ReadsDescriptionsAndSkipsWhatIsNotASkill(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "gmail", "---\nname: gmail\ndescription: Read and send mail. Use when asked.\n---\n# Gmail")
	writeSkillFile(t, dir, "books", "---\nname: books\ndescription: >\n  Книги с Флибусты.\n  Ищет и скачивает.\nallowed: x\n---\n")
	writeSkillFile(t, dir, "quoted", "---\ndescription: \"Quoted text\"\n---\n")
	writeSkillFile(t, dir, "plain", "# No front matter")
	writeSkillFile(t, dir, "_topics", "---\ndescription: mirror\n---\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "synced"), 0o755))

	assert.Equal(t, []Info{
		{Name: "books", Description: "Книги с Флибусты. Ищет и скачивает."},
		{Name: "gmail", Description: "Read and send mail. Use when asked."},
		{Name: "plain"},
		{Name: "quoted", Description: "Quoted text"},
	}, List(dir))
	assert.Nil(t, List(filepath.Join(dir, "missing")))
}

func TestLocalDir(t *testing.T) {
	assert.Equal(t, filepath.Join("/data/1/2", ".claude", "skills"), LocalDir("claude", "/data/1/2"))
	assert.Equal(t, filepath.Join("/data/1/2", ".claude", "skills"), LocalDir("claudish", "/data/1/2"))
	assert.Empty(t, LocalDir("codex", "/data/1/2"))
}
