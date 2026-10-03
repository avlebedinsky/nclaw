// Package skills installs the agent skills bundled with nclaw into CLI skill
// directories without touching skills that are already there.
package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

// Install copies each skill directory from src into dest unless dest already has an
// entry with that name, and returns the names it installed. A missing src is not an error.
func Install(src, dest string) ([]string, error) {
	names, err := listSkills(src)
	if err != nil || len(names) == 0 {
		return nil, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}

	var installed []string
	for _, name := range names {
		if exists(filepath.Join(dest, name)) {
			continue
		}
		if err := installOne(filepath.Join(src, name), dest, name); err != nil {
			return installed, fmt.Errorf("install skill %s: %w", name, err)
		}
		installed = append(installed, name)
	}
	return installed, nil
}

// Dirs returns the skill directories to populate for a CLI backend.
func Dirs(backend, claudeConfigDir, home string) []string {
	dirs := []string{filepath.Join(claudeConfigDir, "skills")}
	switch backend {
	case "codex":
		dirs = append(dirs, filepath.Join(home, ".codex", "skills"))
	case "gemini":
		dirs = append(dirs, filepath.Join(home, ".gemini", "skills"))
	}
	return dirs
}

func listSkills(src string) ([]string, error) {
	entries, err := os.ReadDir(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func installOne(src, dest, name string) error {
	tmp, err := os.MkdirTemp(dest, "."+name+"-")
	if err != nil {
		return err
	}
	if err := copyInto(tmp, src); err != nil {
		removeAll(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dest, name)); err != nil {
		removeAll(tmp)
		return err
	}
	return nil
}

func copyInto(tmp, src string) error {
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	return os.CopyFS(tmp, os.DirFS(src))
}

func removeAll(path string) {
	if err := os.RemoveAll(path); err != nil {
		log.Printf("skills: remove %s: %v", path, err)
	}
}
