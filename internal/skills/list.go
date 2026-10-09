package skills

import (
	"os"
	"path/filepath"
	"strings"
)

// Info describes an installed skill.
type Info struct {
	Name        string
	Description string
}

// List returns the skills in dir sorted by name. Directories without a SKILL.md and those
// whose names start with "." or "_" are not skills and are left out.
func List(dir string) []Info {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var list []Info
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
		if err != nil {
			continue
		}
		list = append(list, Info{Name: name, Description: description(string(data))})
	}
	return list
}

// LocalDir returns the skills directory a CLI backend reads from its working directory,
// or "" when the backend has none.
func LocalDir(backend, workDir string) string {
	switch backend {
	case "claude", "claudish":
		return filepath.Join(workDir, ".claude", "skills")
	default:
		return ""
	}
}

func description(skill string) string {
	if !strings.HasPrefix(skill, "---") {
		return ""
	}
	lines := strings.Split(skill, "\n")
	for i := 1; i < len(lines) && strings.TrimSpace(lines[i]) != "---"; i++ {
		value, ok := strings.CutPrefix(lines[i], "description:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.Trim(value, ">|-") == "" {
			value = indented(lines[i+1:])
		}
		return strings.Trim(value, `"'`)
	}
	return ""
}

func indented(lines []string) string {
	var parts []string
	for _, l := range lines {
		if !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
			break
		}
		parts = append(parts, strings.TrimSpace(l))
	}
	return strings.Join(parts, " ")
}
