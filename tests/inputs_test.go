package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// goOutput runs the go command in a throwaway module and returns what it
// printed. The command is expected to fail.
func goOutput(t *testing.T, src string, args ...string) []byte {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/broken\n\ngo 1.22\n"), 0o644)
	name := "main.go"
	if strings.Contains(src, "func Test") {
		name = "main_test.go"
	}
	os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("go %s succeeded; the test needs a failure", strings.Join(args, " "))
	}
	if len(out) == 0 {
		t.Fatalf("go %s printed nothing", strings.Join(args, " "))
	}
	return out
}

var (
	failedOnce sync.Once
	failed     map[string][]byte
)

// failedOutputs are real outputs of failed builds and runs, the kind of text
// that ends up in a config file when a generating step fails.
func failedOutputs(t *testing.T) map[string][]byte {
	failedOnce.Do(func() {
		failed = map[string][]byte{
			"go build error":  goOutput(t, "package main\n\nfunc main() { undefinedThing() }\n", "build", "./..."),
			"go vet error":    goOutput(t, "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Printf(\"%d\\n\", \"x\") }\n", "vet", "./..."),
			"go run panic":    goOutput(t, "package main\n\nfunc main() { var m map[string]int; m[\"a\"] = 1 }\n", "run", "."),
			"go test failure": goOutput(t, "package main\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) { t.Fatal(\"config: boom\") }\n", "test", "./..."),
			"go run exit 1":   goOutput(t, "package main\n\nimport (\"fmt\"; \"os\")\n\nfunc main() { fmt.Println(\"listen: failed to read config\"); os.Exit(1) }\n", "run", "."),
		}
	})
	return failed
}

func badConfigs(t *testing.T) map[string][]byte {
	prod := readRepo(t, "examples/billing/config.prod.yaml")
	m := map[string][]byte{
		"empty":                  {},
		"only spaces":            []byte("  \n\n\t\n"),
		"only comments":          []byte("# listen: \":8080\"\n# retry:\n"),
		"only a separator":       []byte("---\n"),
		"plain text":             []byte("this is not a config file\n"),
		"binary":                 {0x7f, 'E', 'L', 'F', 0, 1, 2, 3, 0xff, 0xfe},
		"invalid utf-8":          []byte("listen: \"\xff\xfe\"\n"),
		"html error page":        []byte("<!DOCTYPE html>\n<html><body>502 Bad Gateway</body></html>\n"),
		"curl error":             []byte("curl: (7) Failed to connect to config.internal port 443: Connection refused\n"),
		"kubectl error":          []byte("Error from server (NotFound): configmaps \"billing-config\" not found\n"),
		"invalid yaml":           []byte("listen: \":8080\"\ndatabase:\n  url: \"postgres://x\n  max_open_conns: 5\n"),
		"tab indentation":        []byte("database:\n\turl: x\n"),
		"top-level list":         []byte("- listen: \":8080\"\n- retry: {}\n"),
		"top-level scalar":       []byte("42\n"),
		"empty mapping":          []byte("{}\n"),
		"another program":        []byte("apiVersion: v1\nkind: Service\nmetadata:\n  name: billing\n"),
		"unknown anchor":         []byte("listen: *nowhere\n"),
		"prod cut mid-quote":     prod[:12],
		"prod cut inside a list": append(append([]byte{}, prod[:bytes.Index(prod, []byte("- email"))+9]...), []byte("\n  \"x\n")...),
	}
	for name, out := range failedOutputs(t) {
		m[name] = out
	}
	return m
}

func badJSON(t *testing.T) map[string][]byte {
	return map[string][]byte{
		"empty":          {},
		"truncated":      []byte("{\"listen\": \":8080\", \"retry\": {\"attempts\": 3"),
		"trailing text":  []byte("{\"listen\": \":8080\"}\nextra\n"),
		"two objects":    []byte("{\"listen\": \":8080\"}\n{\"listen\": \":9090\"}\n"),
		"yaml not json":  []byte("listen: \":8080\"\n"),
		"array":          []byte("[{\"listen\": \":8080\"}]\n"),
		"null":           []byte("null\n"),
		"empty object":   []byte("{}\n"),
		"unknown object": []byte("{\"kind\": \"Service\"}\n"),
	}
}

func expectRefused(t *testing.T, name string, args []string) {
	t.Helper()
	for _, failOn := range []string{"error", "never"} {
		for _, format := range []string{"text", "json"} {
			a := append([]string{"--fail-on", failOn, "--format", format}, args...)
			r := straykeys(t, nil, a...)
			if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "straykeys: ") {
				t.Errorf("%s (--fail-on %s --format %s): exit %d, want 2 with nothing on stdout\nstdout: %s\nstderr: %s", name, failOn, format, r.code, r.stdout, r.stderr)
			}
		}
	}
}

func TestRefusesBadConfigs(t *testing.T) {
	for name, data := range badConfigs(t) {
		f := writeTemp(t, "config.yaml", data)
		expectRefused(t, name, billingArgs(f))
		// The same file next to a good one still fails the whole run.
		expectRefused(t, name+" with a good file", billingArgs("examples/billing/config.dev.yaml", f))
	}
	for name, data := range badJSON(t) {
		f := writeTemp(t, "config.json", data)
		expectRefused(t, "json: "+name, billingArgs(f))
	}
}

// TestRefusesBadConfigMaps puts each bad config inside a ConfigMap.
func TestRefusesBadConfigMaps(t *testing.T) {
	for name, data := range badConfigs(t) {
		if len(bytes.TrimSpace(data)) == 0 || !utf8ish(data) {
			continue
		}
		cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\ndata:\n  config.yaml: |\n" + indent(indent(string(data)))
		if !strings.HasSuffix(cm, "\n") {
			cm += "\n"
		}
		f := writeTemp(t, "cm.yaml", []byte(cm))
		expectRefused(t, "configmap: "+name, billingArgs("--configmap-key", "config.yaml", f))
	}
	cases := map[string]string{
		"no configmap":    "apiVersion: v1\nkind: Service\nmetadata:\n  name: x\n",
		"key missing":     "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\ndata:\n  other.yaml: |\n    listen: \":1\"\n",
		"no data":         "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n",
		"empty value":     "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\ndata:\n  config.yaml: \"\"\n",
		"value is a list": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\ndata:\n  config.yaml:\n    - a\n",
	}
	for name, body := range cases {
		f := writeTemp(t, "cm.yaml", []byte(body))
		expectRefused(t, "configmap: "+name, billingArgs("--configmap-key", "config.yaml", f))
	}
	for name, data := range failedOutputs(t) {
		f := writeTemp(t, "cm.yaml", data)
		expectRefused(t, "configmap file: "+name, billingArgs("--configmap-key", "config.yaml", f))
	}
}

func utf8ish(b []byte) bool { return bytes.IndexByte(b, 0) < 0 && !bytes.Contains(b, []byte{0xff}) }

func TestRefusesBadFlags(t *testing.T) {
	good := "examples/billing/config.dev.yaml"
	cases := map[string][]string{
		"no type":          {"--dir", billingDir, good},
		"no files":         {"--dir", billingDir, "--type", "Config"},
		"bad decoder":      billingArgs("--decoder", "toml", good),
		"bad fail-on":      billingArgs("--fail-on", "warn", good),
		"bad format":       billingArgs("--format", "xml", good),
		"missing file":     billingArgs("examples/billing/nope.yaml"),
		"directory":        billingArgs("examples/billing"),
		"at missing":       billingArgs("--at", "nope", good),
		"at not a mapping": billingArgs("--at", "listen", good),
		"two stdins":       billingArgs("-", "-"),
		"unknown flag":     billingArgs("--strict", good),
	}
	for name, args := range cases {
		expectRefused(t, name, args)
	}
}

// TestCutConfigs cuts each example config at every byte. A cut must be
// refused or reported, and may look clean only when it falls at the end of
// a line, where YAML can't show that anything is missing. Then fewer keys
// are checked than in the whole file.
func TestCutConfigs(t *testing.T) {
	for _, rel := range []string{"examples/billing/config.dev.yaml", "examples/billing/config.prod.yaml"} {
		data := readRepo(t, rel)
		full := parseReport(t, straykeys(t, nil, billingArgs("--format", "json", "--fail-on", "never", rel)...).stdout)
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for i := 0; i < len(data); i++ {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int) {
				defer wg.Done()
				defer func() { <-sem }()
				cut := data[:i]
				f := writeTemp(t, "cut.yaml", cut)
				r := straykeys(t, nil, billingArgs("--format", "json", f)...)
				switch r.code {
				case 2:
					return
				case 1:
					return
				case 0:
					rep := parseReport(t, r.stdout)
					if !bytes.HasSuffix(cut, []byte("\n")) {
						t.Errorf("%s cut at byte %d (mid-line) looks clean", rel, i)
					}
					if rep.Keys >= full.Keys {
						t.Errorf("%s cut at byte %d checked %d keys, as many as the whole file", rel, i, rep.Keys)
					}
				default:
					t.Errorf("%s cut at byte %d: exit %d", rel, i, r.code)
				}
			}(i)
		}
		wg.Wait()
	}
}

// TestCutJSON cuts a JSON config at every byte: every cut is refused, except
// cuts that only drop trailing white space.
func TestCutJSON(t *testing.T) {
	data := []byte("{\n  \"listen\": \":8080\",\n  \"database\": {\n    \"url\": \"postgres://x\",\n    \"max_open_conns\": 5\n  },\n  \"retry\": {\n    \"attempts\": 3\n  }\n}\n")
	dir := t.TempDir()
	mod := "module example.com/j\n\ngo 1.22\n"
	src := "package j\n\ntype Config struct {\n\tListen   string `json:\"listen\"`\n\tDatabase struct {\n\t\tURL          string `json:\"url\"`\n\t\tMaxOpenConns int    `json:\"max_open_conns\"`\n\t} `json:\"database\"`\n\tRetry struct {\n\t\tAttempts int `json:\"attempts\"`\n\t} `json:\"retry\"`\n}\n"
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644)
	os.WriteFile(filepath.Join(dir, "j.go"), []byte(src), 0o644)
	whole := writeTemp(t, "c.json", data)
	if r := straykeys(t, nil, "--dir", dir, "--type", "Config", whole); r.code != 0 {
		t.Fatalf("whole file: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	for i := 0; i < len(data); i++ {
		cut := data[:i]
		f := writeTemp(t, "c.json", cut)
		r := straykeys(t, nil, "--dir", dir, "--type", "Config", f)
		trailingOnly := len(bytes.TrimSpace(cut)) == len(bytes.TrimSpace(data))
		if r.code != 2 && !trailingOnly {
			t.Errorf("cut at byte %d: exit %d, want 2\n%s", i, r.code, r.stdout)
		}
	}
}
