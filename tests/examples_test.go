package tests

import (
	"strings"
	"testing"
)

func TestBillingProd(t *testing.T) {
	r := straykeys(t, nil, billingArgs("--format", "json", "examples/billing/config.prod.yaml")...)
	if r.code != 1 {
		t.Fatalf("exit %d, want 1\n%s%s", r.code, r.stdout, r.stderr)
	}
	got := parseReport(t, r.stdout).codes()
	want := "unknown-key:4:database.maxOpenConns unknown-key:8:retry.backof unknown-key:14:invoices.notify[0].onFailed unknown-key:18:max_open_conns"
	if got != want {
		t.Errorf("findings\n got %s\nwant %s", got, want)
	}
}

func TestBillingDevIsQuiet(t *testing.T) {
	r := straykeys(t, nil, billingArgs("examples/billing/config.dev.yaml")...)
	if r.code != 0 || !strings.Contains(r.stdout, "No problems: all 17 keys") {
		t.Fatalf("exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

func TestBillingConfigMap(t *testing.T) {
	r := straykeys(t, nil, billingArgs("--format", "json", "--configmap-key", "config.yaml", "examples/billing/deploy/configmap.yaml")...)
	if r.code != 1 {
		t.Fatalf("exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	got := parseReport(t, r.stdout).codes()
	want := "duration-without-unit:11:database.conn_timeout unknown-key:16:invoices.currancy"
	if got != want {
		t.Errorf("findings\n got %s\nwant %s", got, want)
	}
}

// TestFailFlag checks exit codes with and without --fail-on.
func TestFailFlag(t *testing.T) {
	cases := []struct {
		file   string
		failOn string
		want   int
	}{
		{"examples/billing/config.prod.yaml", "", 1},
		{"examples/billing/config.prod.yaml", "error", 1},
		{"examples/billing/config.prod.yaml", "never", 0},
		{"examples/billing/config.dev.yaml", "", 0},
		{"examples/billing/config.dev.yaml", "error", 0},
		{"examples/billing/config.dev.yaml", "never", 0},
	}
	for _, c := range cases {
		for _, format := range []string{"text", "json"} {
			args := billingArgs("--format", format)
			if c.failOn != "" {
				args = append(args, "--fail-on", c.failOn)
			}
			r := straykeys(t, nil, append(args, c.file)...)
			if r.code != c.want {
				t.Errorf("%s --fail-on %q --format %s: exit %d, want %d", c.file, c.failOn, format, r.code, c.want)
			}
		}
	}
}

func TestTwoFilesTogether(t *testing.T) {
	r := straykeys(t, nil, billingArgs("examples/billing/config.dev.yaml", "examples/billing/config.prod.yaml")...)
	if r.code != 1 || !strings.Contains(r.stdout, "4 errors in 1 of 2 configs") {
		t.Fatalf("exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

func TestStdin(t *testing.T) {
	r := straykeys(t, readRepo(t, "examples/billing/config.prod.yaml"), billingArgs("-")...)
	if r.code != 1 || !strings.Contains(r.stdout, "stdin:8  retry.backof") {
		t.Fatalf("exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

func TestAt(t *testing.T) {
	nested := "app:\n" + indent(string(readRepo(t, "examples/billing/config.prod.yaml"))) + "other_tool:\n  enabled: true\n"
	f := writeTemp(t, "nested.yaml", []byte(nested))
	r := straykeys(t, nil, billingArgs("--format", "json", "--at", "app", f)...)
	if r.code != 1 {
		t.Fatalf("exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if got := parseReport(t, r.stdout).codes(); !strings.Contains(got, "unknown-key:9:retry.backof") {
		t.Errorf("findings %s", got)
	}
}

func TestAllow(t *testing.T) {
	r := straykeys(t, nil, billingArgs("--format", "json", "--allow", "max_open_conns", "--allow", "retry.backof", "examples/billing/config.prod.yaml")...)
	got := parseReport(t, r.stdout).codes()
	if strings.Contains(got, "max_open_conns") || strings.Contains(got, "backof") || !strings.Contains(got, "maxOpenConns") {
		t.Errorf("findings %s", got)
	}
}

func TestNoFinalNewline(t *testing.T) {
	data := strings.TrimRight(string(readRepo(t, "examples/billing/config.dev.yaml")), "\n")
	f := writeTemp(t, "c.yaml", []byte(data))
	for _, failOn := range []string{"error", "never"} {
		r := straykeys(t, nil, billingArgs("--fail-on", failOn, f)...)
		if strings.Contains(r.stdout, "No problems") || !strings.Contains(r.stdout, "no-final-newline") {
			t.Errorf("--fail-on %s: a file without a final newline gave\n%s", failOn, r.stdout)
		}
		if failOn == "error" && r.code != 1 {
			t.Errorf("exit %d, want 1", r.code)
		}
	}
}

func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.SplitAfter(s, "\n") {
		if l != "" {
			b.WriteString("  " + l)
		}
	}
	return b.String()
}
