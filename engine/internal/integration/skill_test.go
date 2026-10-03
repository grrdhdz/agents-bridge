package integration

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The embedded copy is what the app installs; it must match the repo skill.
func TestEmbeddedSkillMatchesRepository(t *testing.T) {
	repo, err := os.ReadFile(filepath.Join("..", "..", "..", ".agents", "skills", "agents-bridge", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(repo, skillDoc) {
		t.Fatal("engine/internal/integration/skill/SKILL.md desactualizada: cópiala desde .agents/skills/agents-bridge/SKILL.md")
	}
}

func TestEnsureInstallsSkillReplacingOwnSymlink(t *testing.T) {
	o := isolatedEnsure(t)
	ctx := context.Background()
	// A previous manual install linked the skill into a checkout.
	checkout := filepath.Join(o.Home, "checkout", "agents-bridge")
	os.MkdirAll(checkout, 0755)
	os.WriteFile(filepath.Join(checkout, "SKILL.md"), []byte("---\nname: agents-bridge\n---\nvieja\n"), 0644)
	agents := filepath.Join(o.Home, ".agents", "skills")
	os.MkdirAll(agents, 0755)
	if err := os.Symlink(checkout, filepath.Join(agents, "agents-bridge")); err != nil {
		t.Skip("symlinks no disponibles:", err)
	}
	r, err := Ensure(ctx, o)
	if err != nil || !r.Skill.Installed || r.Skill.Error != "" {
		t.Fatal(r.Skill, err)
	}
	for _, dir := range []string{filepath.Join(agents, "agents-bridge"), filepath.Join(o.Home, ".claude", "skills", "agents-bridge")} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			t.Fatal("skill no es un directorio propio:", dir, err)
		}
		got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if !bytes.Equal(got, skillDoc) {
			t.Fatal("contenido de skill incorrecto en", dir)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(checkout, "SKILL.md")); string(b) != "---\nname: agents-bridge\n---\nvieja\n" {
		t.Fatal("se modificó el destino del enlace")
	}
	if r, err = Ensure(ctx, o); err != nil || r.Changed {
		t.Fatal("ensure repetido escribió", r, err)
	}
}

func TestEnsureSkillDedupesLinkedClaudeDirAndKeepsForeign(t *testing.T) {
	o := isolatedEnsure(t)
	agents := filepath.Join(o.Home, ".agents", "skills")
	os.MkdirAll(agents, 0755)
	os.MkdirAll(filepath.Join(o.Home, ".claude"), 0755)
	if err := os.Symlink(agents, filepath.Join(o.Home, ".claude", "skills")); err != nil {
		t.Skip("symlinks no disponibles:", err)
	}
	foreign := filepath.Join(agents, "agents-bridge")
	os.MkdirAll(foreign, 0755)
	os.WriteFile(filepath.Join(foreign, "SKILL.md"), []byte("---\nname: otra\n---\n"), 0644)
	r, _ := Ensure(context.Background(), o)
	if r.Skill.Installed || r.Skill.Error == "" {
		t.Fatal("sobrescribió una skill ajena", r.Skill)
	}
	if len(r.Skill.Paths) != 1 {
		t.Fatal("rutas duplicadas", r.Skill.Paths)
	}
	if b, _ := os.ReadFile(filepath.Join(foreign, "SKILL.md")); string(b) != "---\nname: otra\n---\n" {
		t.Fatal("modificó la skill ajena")
	}
}
