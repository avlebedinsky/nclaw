package streamjson

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/nickalie/nclaw/internal/cli"
)

const maxDetailRunes = 80

type toolInput struct {
	Command      string `json:"command"`
	Description  string `json:"description"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Pattern      string `json:"pattern"`
	URL          string `json:"url"`
	Query        string `json:"query"`
	Skill        string `json:"skill"`
}

func toolEvent(name string, input json.RawMessage) cli.ToolEvent {
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		server, tool, _ := strings.Cut(rest, "__")
		return cli.ToolEvent{Name: server, Detail: tool}
	}

	var in toolInput
	if len(input) > 0 && json.Unmarshal(input, &in) != nil {
		return cli.ToolEvent{Name: name}
	}
	return cli.ToolEvent{Name: name, Detail: truncateRunes(toolDetail(name, &in), maxDetailRunes)}
}

func toolDetail(name string, in *toolInput) string {
	switch name {
	case "Bash":
		return firstNonEmpty(in.Description, firstLine(in.Command))
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		return filepath.Base(firstNonEmpty(in.FilePath, in.NotebookPath))
	case "Grep", "Glob":
		return in.Pattern
	case "WebFetch":
		return host(in.URL)
	default:
		return firstNonEmpty(in.Description, in.Query, in.Skill)
	}
}

func host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
