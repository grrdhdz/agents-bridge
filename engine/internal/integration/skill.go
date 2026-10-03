package integration

import (
	"bytes"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
)

// skillDoc is a copy of .agents/skills/agents-bridge/SKILL.md; go:embed cannot
// reach outside the module, and a test keeps both files identical.
//
//go:embed skill/SKILL.md
var skillDoc []byte

const skillName = "agents-bridge"

type SkillStatus struct {
	Installed bool     `json:"installed"`
	Paths     []string `json:"paths"`
	Error     string   `json:"error,omitempty"`
}

// skillDirs lists where Codex (~/.agents/skills) and Claude Code
// (~/.claude/skills) look for user skills. A Claude skills directory linked to
// the shared one counts once.
func (o EnsureOptions) skillDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	for _, parent := range []string{filepath.Join(o.Home, ".agents", "skills"), filepath.Join(o.Home, ".claude", "skills")} {
		key := parent
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			key = resolved
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		dirs = append(dirs, filepath.Join(parent, skillName))
	}
	return dirs
}

// ownedSkill reports whether a SKILL.md belongs to agents-bridge, by the name
// in its frontmatter; any other skill in that directory is left untouched.
func ownedSkill(doc []byte) bool {
	return bytes.HasPrefix(doc, []byte("---\n")) && bytes.Contains(doc, []byte("\nname: "+skillName+"\n"))
}

func skillStatus(o EnsureOptions) SkillStatus {
	s := SkillStatus{Installed: true, Paths: o.skillDirs()}
	for _, dir := range s.Paths {
		info, err := os.Lstat(dir)
		doc, readErr := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil || !info.IsDir() || readErr != nil || !bytes.Equal(doc, skillDoc) {
			s.Installed = false
		}
	}
	return s
}

func installSkill(dir string) (bool, error) {
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return false, err
	case info.Mode()&os.ModeSymlink != 0:
		// An earlier manual install linked a checkout. Replace only the link;
		// a link to someone else's skill is kept.
		doc, readErr := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if readErr == nil && !ownedSkill(doc) {
			return false, errors.New("enlace de skill ajeno en " + dir + "; no se reemplazó")
		}
		if err := os.Remove(dir); err != nil {
			return false, err
		}
	case !info.IsDir():
		return false, errors.New("destino de skill ajeno en " + dir + "; no se reemplazó")
	default:
		doc, readErr := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if readErr == nil && bytes.Equal(doc, skillDoc) {
			return false, nil
		}
		if readErr == nil && !ownedSkill(doc) {
			return false, errors.New("skill ajena en " + dir + "; no se reemplazó")
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, err
	}
	if err := atomicFile(filepath.Join(dir, "SKILL.md"), skillDoc, 0644); err != nil {
		return false, err
	}
	return true, nil
}

func ensureSkill(o EnsureOptions) (SkillStatus, bool) {
	changed := false
	var failure error
	for _, dir := range o.skillDirs() {
		c, err := installSkill(dir)
		changed = changed || c
		if err != nil && failure == nil {
			failure = err
		}
	}
	s := skillStatus(o)
	if failure != nil {
		s.Error = failure.Error()
	}
	return s, changed
}
