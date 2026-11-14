package tests

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstalledPluginBuilds copies the repository the way Claude Code
// installs the plugin (the marketplace source is the repository root), then
// runs the build command from SKILL.md with no network.
func TestInstalledPluginBuilds(t *testing.T) {
	skill, err := os.ReadFile("../skills/checking-config-keys-against-go-structs/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	var build string
	for _, l := range strings.Split(string(skill), "\n") {
		if strings.Contains(l, "go build") && strings.Contains(l, "CLAUDE_PLUGIN_ROOT") {
			build = strings.TrimSpace(l)
		}
	}
	if build == "" {
		t.Fatal("no build command in SKILL.md")
	}
	root := t.TempDir()
	err = filepath.WalkDir("..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("..", p)
		if d.IsDir() && (rel == ".git" || rel == filepath.Join("tests", "decoders")) {
			return filepath.SkipDir
		}
		if d.Type()&fs.ModeSymlink != 0 {
			t.Errorf("symlink in the repository: %s", rel)
			return nil
		}
		target := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	cmd := exec.Command("sh", "-c", build+` && "${TMPDIR:-/tmp}/straykeys" --version`)
	cmd.Env = append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+root, "TMPDIR="+tmp, "GOPROXY=off", "GOFLAGS=-mod=vendor", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "straykeys 0.1.0") {
		t.Fatalf("%s: %v\n%s", build, err, out)
	}
}
