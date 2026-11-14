package tests

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const srcConfig = `package svc

import "time"

// Config is what config.yaml decodes into.
type Config struct {
	Listen  string        ` + "`yaml:\"listen\"`" + `
	Timeout time.Duration ` + "`yaml:\"timeout\"`" + `
	DB      DB            ` + "`yaml:\"db\"`" + `
}

type DB struct {
	URL      string ` + "`yaml:\"url\"`" + `
	MaxConns int    ` + "`yaml:\"max_conns\"`" + `
}
`

const srcYAML = "listen: \":8080\"\ntimeout: 5s\ndb:\n  url: postgres://x\n  max_conns: 4\n  max_con: 5\n"

// module writes a throwaway module with the given files.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if _, ok := files["go.mod"]; !ok {
		files["go.mod"] = "module example.com/svc\n\ngo 1.22\n"
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSourceGood(t *testing.T) {
	dir := module(t, map[string]string{"svc.go": srcConfig})
	cfg := writeTemp(t, "c.yaml", []byte(srcYAML))
	r := straykeys(t, nil, "--dir", dir, "--type", "Config", "--format", "json", cfg)
	if r.code != 1 {
		t.Fatalf("exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if got := parseReport(t, r.stdout).codes(); got != "unknown-key:6:db.max_con" {
		t.Errorf("findings %s", got)
	}
}

func TestRefusesBadSources(t *testing.T) {
	failedBuild := string(failedOutputs(t)["go build error"])
	cases := map[string]map[string]string{
		"empty go file":             {"svc.go": ""},
		"garbage go file":           {"svc.go": "this is not go\n"},
		"go build output as source": {"svc.go": failedBuild},
		"go vet output as source":   {"svc.go": string(failedOutputs(t)["go vet error"])},
		"no go.mod":                 {"go.mod": "", "svc.go": srcConfig},
		"garbage go.mod":            {"go.mod": "not a go.mod\n", "svc.go": srcConfig},
		"type missing":              {"svc.go": "package svc\n\ntype Other struct{ A int `yaml:\"a\"` }\n"},
		"no struct types":           {"svc.go": "package svc\n\nvar X = 1\n"},
		"not a struct":              {"svc.go": "package svc\n\ntype Config map[string]any\n"},
		"not a type":                {"svc.go": "package svc\n\nvar Config = struct{ A int }{}\n"},
		"generic":                   {"svc.go": "package svc\n\ntype Config[T any] struct{ A T `yaml:\"a\"` }\n"},
		"undefined field type":      {"svc.go": "package svc\n\ntype Config struct {\n\tListen string `yaml:\"listen\"`\n\tDB Database `yaml:\"db\"`\n}\n"},
		"syntax error in sibling":   {"svc.go": srcConfig, "other.go": "package svc\n\nfunc broken( {\n"},
		"two packages in one dir":   {"svc.go": srcConfig, "other.go": "package other\n"},
		"only test files":           {"svc_test.go": "package svc\n\ntype Config struct{ A int `yaml:\"a\"` }\n"},
		"mixed tags":                {"svc.go": "package svc\n\ntype Config struct {\n\tListen string `yaml:\"listen\" json:\"listen\"`\n\tDB string `yaml:\"db\" json:\"db\"`\n}\n"},
		"no tags":                   {"svc.go": "package svc\n\ntype Config struct {\n\tListen string\n\tDB string\n}\n"},
		"decodes itself": {"svc.go": "package svc\n\nimport \"errors\"\n\ntype Config struct {\n\tListen string `yaml:\"listen\"`\n}\n\n" +
			"func (c *Config) UnmarshalYAML(f func(any) error) error {\n\tvar m map[string]string\n\tif err := f(&m); err != nil {\n\t\treturn err\n\t}\n\tif m[\"addr\"] == \"\" {\n\t\treturn errors.New(\"addr\")\n\t}\n\tc.Listen = m[\"addr\"]\n\treturn nil\n}\n"},
		"bad yaml flag":      {"svc.go": "package svc\n\ntype Config struct {\n\tListen string `yaml:\"listen,omitempty,required\"`\n}\n"},
		"duplicate yaml key": {"svc.go": "package svc\n\ntype Config struct {\n\tListen string `yaml:\"listen\"`\n\tAddr string `yaml:\"listen\"`\n}\n"},
	}
	cfg := writeTemp(t, "c.yaml", []byte(srcYAML))
	for name, files := range cases {
		dir := module(t, files)
		if files["go.mod"] == "" {
			os.Remove(filepath.Join(dir, "go.mod"))
		}
		expectRefused(t, name, []string{"--dir", dir, "--type", "Config", cfg})
	}
	expectRefused(t, "missing dir", []string{"--dir", filepath.Join(t.TempDir(), "nope"), "--type", "Config", cfg})
	expectRefused(t, "empty dir", []string{"--dir", t.TempDir(), "--type", "Config", cfg})
	expectRefused(t, "file as dir", []string{"--dir", cfg, "--type", "Config", cfg})
	dir := module(t, map[string]string{"svc.go": srcConfig})
	expectRefused(t, "bad type flag", []string{"--dir", dir, "--type", "./svc.", cfg})
	two := module(t, map[string]string{"svc.go": srcConfig, "sub/sub.go": strings.Replace(srcConfig, "package svc", "package sub", 1)})
	expectRefused(t, "pattern matching two packages", []string{"--dir", two, "--type", "./....Config", cfg})
}

// TestCutSource cuts the Go file at every byte. Each cut must be refused,
// or, when only the end of the file is gone and it still type-checks, give
// exactly the result of the whole file.
func TestCutSource(t *testing.T) {
	cfg := writeTemp(t, "c.yaml", []byte(srcYAML))
	whole := straykeys(t, nil, "--dir", module(t, map[string]string{"svc.go": srcConfig}), "--type", "Config", "--format", "json", cfg)
	want := parseReport(t, whole.stdout).codes()
	step := 1
	if testing.Short() {
		step = 7
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := 0; i < len(srcConfig); i += step {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			dir := module(t, map[string]string{"svc.go": srcConfig[:i]})
			r := straykeys(t, nil, "--dir", dir, "--type", "Config", "--format", "json", cfg)
			if r.code == 2 {
				return
			}
			if got := parseReport(t, r.stdout).codes(); got != want || strings.TrimSpace(srcConfig[i:]) != "" {
				t.Errorf("cut at byte %d of %d: exit %d, findings %q (whole file: %q)", i, len(srcConfig), r.code, got, want)
			}
		}(i)
	}
	wg.Wait()
}

// TestCatchAllNeedsARealKey: a struct whose inline map takes any key must
// still refuse a file with none of its named fields, such as saved error text.
func TestCatchAllNeedsARealKey(t *testing.T) {
	src := "package svc\n\ntype Config struct {\n\tName  string         `yaml:\"name\"`\n\tExtra map[string]any `yaml:\",inline\"`\n}\n"
	dir := module(t, map[string]string{"svc.go": src})
	for name, data := range failedOutputs(t) {
		f := writeTemp(t, "c.yaml", data)
		expectRefused(t, "catch-all: "+name, []string{"--dir", dir, "--type", "Config", f})
	}
	f := writeTemp(t, "c.yaml", []byte("Error from server (NotFound): configmaps \"x\" not found\n"))
	expectRefused(t, "catch-all: kubectl error", []string{"--dir", dir, "--type", "Config", f})
	good := writeTemp(t, "c.yaml", []byte("name: a\nanything: 1\n"))
	if r := straykeys(t, nil, "--dir", dir, "--type", "Config", good); r.code != 0 {
		t.Errorf("a config with a named field and extra keys: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

func TestEmptyStdin(t *testing.T) {
	for _, failOn := range []string{"error", "never"} {
		r := straykeys(t, []byte{}, billingArgs("--fail-on", failOn, "-")...)
		if r.code != 2 || r.stdout != "" {
			t.Errorf("empty stdin (--fail-on %s): exit %d, stdout %q", failOn, r.code, r.stdout)
		}
	}
}
