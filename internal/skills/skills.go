// Package skills installs the agent skills bundled with nclaw into CLI skill
// directories and keeps the copies it made up to date, without touching skills
// that were changed locally.
package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

// Report lists what Install did with the bundled skills.
type Report struct {
	Installed []string
	Updated   []string
	Kept      []string
}

type action int

const (
	upToDate action = iota
	install
	update
	keep
)

type state map[string]map[string]string

// Install brings the skills bundled in src into dest. A missing skill is copied. A skill
// still identical to the copy nclaw made earlier is replaced when the bundled version
// changes; one changed since, or a link managed elsewhere, is kept as is. stateFile
// records the copies nclaw made. A missing src is not an error.
func Install(src, dest, stateFile string) (Report, error) {
	names, err := listSkills(src)
	if err != nil || len(names) == 0 {
		return Report{}, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return Report{}, err
	}

	st := loadState(stateFile)
	known := st.forDest(dest)
	var rep Report
	for _, name := range names {
		if err := syncSkill(src, dest, name, known, &rep); err != nil {
			return rep, errors.Join(fmt.Errorf("install skill %s: %w", name, err), saveState(stateFile, st))
		}
	}
	return rep, saveState(stateFile, st)
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

func syncSkill(src, dest, name string, known map[string]string, rep *Report) error {
	skillSrc := filepath.Join(src, name)
	bundled, err := hashDir(skillSrc)
	if err != nil {
		return err
	}

	switch decide(filepath.Join(dest, name), known[name], bundled) {
	case install:
		if err := installOne(skillSrc, dest, name); err != nil {
			return err
		}
		rep.Installed = append(rep.Installed, name)
	case update:
		if err := replaceOne(skillSrc, dest, name); err != nil {
			return err
		}
		rep.Updated = append(rep.Updated, name)
	case keep:
		rep.Kept = append(rep.Kept, name)
		return nil
	}
	known[name] = bundled
	return nil
}

func decide(target, recorded, bundled string) action {
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return install
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return keep
	}

	current, err := hashDir(target)
	switch {
	case err != nil:
		return keep
	case current == bundled:
		return upToDate
	case current == recorded:
		return update
	default:
		return keep
	}
}

func hashDir(dir string) (string, error) {
	fsys := os.DirFS(dir)
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(h, "%s\x00%d\x00", path, len(data)); err != nil {
			return err
		}
		_, err = h.Write(data)
		return err
	})
	return hex.EncodeToString(h.Sum(nil)), err
}

func loadState(path string) state {
	st := state{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st
	}
	if err == nil {
		err = json.Unmarshal(data, &st)
	}
	if err != nil {
		log.Printf("skills: ignoring unreadable state %s: %v", path, err)
		return state{}
	}
	return st
}

func (st state) forDest(dest string) map[string]string {
	if st[dest] == nil {
		st[dest] = map[string]string{}
	}
	return st[dest]
}

func saveState(path string, st state) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

func installOne(src, dest, name string) error {
	staged, err := stage(src, dest, name)
	if err != nil {
		return err
	}
	if err := os.Rename(staged, filepath.Join(dest, name)); err != nil {
		removeAll(staged)
		return err
	}
	return nil
}

func replaceOne(src, dest, name string) error {
	staged, err := stage(src, dest, name)
	if err != nil {
		return err
	}
	target := filepath.Join(dest, name)
	retired := staged + "-old"
	if err := os.Rename(target, retired); err != nil {
		removeAll(staged)
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		if rbErr := os.Rename(retired, target); rbErr != nil {
			log.Printf("skills: restore %s: %v", target, rbErr)
		}
		removeAll(staged)
		return err
	}
	removeAll(retired)
	return nil
}

func stage(src, dest, name string) (string, error) {
	tmp, err := os.MkdirTemp(dest, "."+name+"-")
	if err != nil {
		return "", err
	}
	if err := copyInto(tmp, src); err != nil {
		removeAll(tmp)
		return "", err
	}
	return tmp, nil
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
