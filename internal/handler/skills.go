package handler

import (
	"os"
	"path/filepath"
	"strings"
)

// loadSkillsPrompt reads all SKILL.md files from the skills directory
// and returns their concatenated content as a prompt section.
func loadSkillsPrompt(skillsDir string) string {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return ""
	}

	var parts []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillFile := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}

		parts = append(parts, strings.TrimSpace(string(data)))
	}

	if len(parts) == 0 {
		return ""
	}

	return "# Available Skills\n\n" + strings.Join(parts, "\n\n---\n\n")
}
