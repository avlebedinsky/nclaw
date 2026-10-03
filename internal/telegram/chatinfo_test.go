package telegram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveChatName_RecordsAndReportsChanges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chat")

	changed, err := SaveChatName(dir, "  Семья \n и дом ")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "Семья и дом", ChatName(dir))

	changed, err = SaveChatName(dir, "Семья и дом")
	require.NoError(t, err)
	assert.False(t, changed)

	changed, err = SaveChatName(dir, "Дом")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "Дом", ChatName(dir))
}

func TestSaveTopicName_IgnoresEmptyName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "topic")

	changed, err := SaveTopicName(dir, "  ")
	require.NoError(t, err)
	assert.False(t, changed)
	assert.NoDirExists(t, dir)
	assert.Empty(t, TopicName(dir))
}

func TestSaveTopicName_TruncatesLongNames(t *testing.T) {
	dir := t.TempDir()

	_, err := SaveTopicName(dir, strings.Repeat("я", 200))
	require.NoError(t, err)

	assert.Equal(t, strings.Repeat("я", maxNameRunes), TopicName(dir))
}

func TestChatAndTopicNamesAreSeparate(t *testing.T) {
	dir := t.TempDir()
	_, err := SaveChatName(dir, "Группа")
	require.NoError(t, err)

	assert.Empty(t, TopicName(dir))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestIsPrivateChat(t *testing.T) {
	assert.True(t, IsPrivateChat(375321681))
	assert.False(t, IsPrivateChat(-1003874128370))
}
