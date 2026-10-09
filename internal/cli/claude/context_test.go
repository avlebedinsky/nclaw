package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nickalie/nclaw/internal/cli"
)

const transcript = `{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"assistant","message":{"model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_creation_input_tokens":10771,"cache_read_input_tokens":8368,"output_tokens":42}}}
not json
{"type":"assistant","message":{"model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0}}}
`

func TestUsageFromTranscript_TakesTheLastReply(t *testing.T) {
	usage, ok := usageFromTranscript([]byte(transcript))

	require.True(t, ok)
	assert.Equal(t, cli.ContextUsage{Tokens: 19183, Model: "claude-opus-5-5"}, usage)

	compacted := transcript + `{"type":"system","subtype":"compact_boundary","compactMetadata":{"preTokens":19184,"postTokens":1038}}` + "\n"
	usage, ok = usageFromTranscript([]byte(compacted))
	require.True(t, ok)
	assert.Equal(t, 1038, usage.Tokens)

	_, ok = usageFromTranscript([]byte(`{"type":"user","message":{"content":"hi"}}`))
	assert.False(t, ok)
}

func TestContextUsage_ReadsTheLatestTranscript(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "400000")
	work := t.TempDir()
	_, ok := ContextUsage(work)
	assert.False(t, ok)
	projectDir, err := sessionDir(work)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(projectDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "s.jsonl"), []byte(transcript), 0o644))

	usage, ok := ContextUsage(work)

	require.True(t, ok)
	assert.Equal(t, cli.ContextUsage{Tokens: 19183, CompactAt: 400000, Model: "claude-opus-5-5"}, usage)
	assert.Equal(t, "/compact", NewProvider().CompactPrompt())
}
