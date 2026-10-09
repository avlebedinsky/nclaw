package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/nickalie/nclaw/internal/cli"
)

type transcriptEntry struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message struct {
		Model string      `json:"model"`
		Usage *tokenUsage `json:"usage"`
	} `json:"message"`
	CompactMetadata *struct {
		PostTokens int `json:"postTokens"`
	} `json:"compactMetadata"`
}

type tokenUsage struct {
	InputTokens   int `json:"input_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
	OutputTokens  int `json:"output_tokens"`
}

// ContextUsage reads how full the latest conversation of the working directory dir was at
// its last reply, from the token counts the CLI records with every reply and compaction.
func ContextUsage(dir string) (cli.ContextUsage, bool) {
	projectDir, err := sessionDir(dir)
	if err != nil {
		return cli.ContextUsage{}, false
	}
	latest, ok := latestTranscript(projectDir)
	if !ok {
		return cli.ContextUsage{}, false
	}
	data, err := os.ReadFile(filepath.Join(projectDir, latest.Name()))
	if err != nil {
		return cli.ContextUsage{}, false
	}
	usage, found := usageFromTranscript(data)
	usage.CompactAt = compactWindow()
	return usage, found
}

func compactWindow() int {
	n, err := strconv.Atoi(os.Getenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW"))
	if err != nil {
		return 0
	}
	return n
}

func usageFromTranscript(data []byte) (cli.ContextUsage, bool) {
	var usage cli.ContextUsage
	found := false
	for line := range bytes.Lines(data) {
		var e transcriptEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch {
		case e.Type == "assistant" && e.Message.Usage != nil && e.Message.Model != "<synthetic>":
			u := e.Message.Usage
			usage.Tokens = u.InputTokens + u.CacheCreation + u.CacheRead + u.OutputTokens
			usage.Model = e.Message.Model
			found = true
		case e.Subtype == "compact_boundary" && e.CompactMetadata != nil:
			usage.Tokens = e.CompactMetadata.PostTokens
			found = true
		}
	}
	return usage, found
}
