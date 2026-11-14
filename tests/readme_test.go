package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var filepathAbs = filepath.Abs

// TestReadme runs every "$ " line in the README's indented code blocks from
// the repository root and compares its output with the lines that follow.
func TestReadme(t *testing.T) {
	data, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	found := 0
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "    $ ") {
			continue
		}
		cmdline := strings.TrimPrefix(lines[i], "    $ ")
		var want []string
		for j := i + 1; j < len(lines); j++ {
			l := lines[j]
			if l == "" && j+1 < len(lines) && strings.HasPrefix(lines[j+1], "    ") && !strings.HasPrefix(lines[j+1], "    $ ") {
				want = append(want, "")
				continue
			}
			if !strings.HasPrefix(l, "    ") || strings.HasPrefix(l, "    $ ") {
				break
			}
			want = append(want, strings.TrimPrefix(l, "    "))
		}
		found++
		if !strings.HasPrefix(cmdline, "go run ./cmd/straykeys ") {
			t.Errorf("README command doesn't start with go run ./cmd/straykeys: %s", cmdline)
			continue
		}
		cmd := exec.Command("sh", "-c", strings.Replace(cmdline, "go run ./cmd/straykeys", tool, 1))
		cmd.Dir = ".."
		out, err := cmd.Output()
		if err != nil {
			t.Errorf("%s: %v", cmdline, err)
		}
		if got := strings.TrimRight(string(out), "\n"); got != strings.TrimRight(strings.Join(want, "\n"), "\n") {
			t.Errorf("%s: README output differs.\n--- got\n%s\n--- README\n%s", cmdline, got, strings.Join(want, "\n"))
		}
	}
	if found < 2 {
		t.Errorf("found %d commands in the README, expected at least 2", found)
	}
}

// TestReadmeRefusal checks the refusal message quoted in the README against
// a real run on kubectl's error text.
func TestReadmeRefusal(t *testing.T) {
	dir := t.TempDir()
	out := "Error from server (NotFound): configmaps \"billing-config\" not found\n"
	if err := os.WriteFile(dir+"/out.yaml", []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepathAbs("../examples/billing")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(tool, "--dir", abs, "--type", "Config", "out.yaml")
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 || len(stdout) != 0 {
		t.Fatalf("exit %v, stdout %q", err, stdout)
	}
	readme, _ := os.ReadFile("../README.md")
	if !strings.Contains(string(readme), "    "+strings.TrimSpace(stderr.String())+"\n") {
		t.Errorf("README doesn't quote the refusal:\n%s", stderr.String())
	}
}
