// Package tests runs the straykeys binary end to end: on the examples, on
// input it must refuse, and on files cut off at every byte.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var tool string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "straykeys-tests")
	if err != nil {
		panic(err)
	}
	tool = filepath.Join(dir, "straykeys")
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", tool, "./cmd/straykeys")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	stdout, stderr string
	code           int
}

// straykeys runs the tool from the repository root.
func straykeys(t *testing.T, stdin []byte, args ...string) result {
	t.Helper()
	cmd := exec.Command(tool, args...)
	cmd.Dir = ".."
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running straykeys: %v", err)
	}
	return result{out.String(), errb.String(), code}
}

type report struct {
	Configs  []string `json:"configs"`
	Keys     int      `json:"keysChecked"`
	Findings []struct {
		Code string `json:"code"`
		Line int    `json:"line"`
		Key  string `json:"key"`
	} `json:"findings"`
}

func parseReport(t *testing.T, s string) report {
	t.Helper()
	var r report
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		t.Fatalf("json output: %v\n%s", err, s)
	}
	return r
}

func (r report) codes() string {
	var cs []string
	for _, f := range r.Findings {
		cs = append(cs, fmt.Sprintf("%s:%d:%s", f.Code, f.Line, f.Key))
	}
	return strings.Join(cs, " ")
}

const billingDir = "examples/billing"

func billingArgs(extra ...string) []string {
	return append([]string{"--dir", billingDir, "--type", "Config"}, extra...)
}

// writeTemp writes data to a new file and returns its path relative to the
// repository root's parent, as an absolute path.
func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readRepo(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
