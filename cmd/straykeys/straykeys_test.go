package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runJSON(t *testing.T, args ...string) (Report, int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"--format", "json"}, args...), &out, &errb)
	var r Report
	if code != 2 {
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("output: %v\n%s", err, out.String())
		}
	}
	return r, code, errb.String()
}

func codes(r Report) string {
	var cs []string
	for _, f := range r.Findings {
		cs = append(cs, f.Code+":"+f.Key)
	}
	return strings.Join(cs, " ")
}

func TestViperGetKeysCount(t *testing.T) {
	r, code, stderr := runJSON(t, "--dir", "testdata/viperapp", "--type", "./internal/config.Config", "testdata/viperapp/config.yaml")
	if code != 1 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := "unknown-key:server.wrokers unknown-key:log.format duration-without-unit:timeout"
	if got := codes(r); got != want {
		t.Errorf("findings\n got %s\nwant %s", got, want)
	}
	if r.ViperGets != 4 {
		t.Errorf("keys added from viper.Get calls: %d, want 4 (log, log.level, plugins, server.workers)", r.ViperGets)
	}
	r, _, _ = runJSON(t, "--dir", "testdata/viperapp", "--type", "./internal/config.Config", "--no-viper-gets", "testdata/viperapp/config.yaml")
	want = "unknown-key:server.workers unknown-key:server.wrokers unknown-key:log unknown-key:plugins duration-without-unit:timeout"
	if got := codes(r); got != want {
		t.Errorf("--no-viper-gets findings\n got %s\nwant %s", got, want)
	}
}

func TestPlainCopyPattern(t *testing.T) {
	r, code, stderr := runJSON(t, "--dir", "testdata/plain", "--type", "Config", "--decoder", "yaml", "testdata/plain/config.yaml")
	if code != 1 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := codes(r); got != "unknown-key:prot" {
		t.Errorf("findings %s", got)
	}
	if r.PlainCopies != 1 || r.Opaque != 1 {
		t.Errorf("plain copies %d, opaque %d; want 1 and 1", r.PlainCopies, r.Opaque)
	}
}

func TestJSONDuplicates(t *testing.T) {
	r, code, stderr := runJSON(t, "--dir", "testdata/plain", "--type", "Config", "--decoder", "json", "testdata/plain/config.json")
	if code != 1 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := codes(r); got != "duplicate-key:port duplicate-key:Host" {
		t.Errorf("findings %s", got)
	}
}

func TestSplitTypeFlag(t *testing.T) {
	cases := map[string][2]string{
		"Config":                        {".", "Config"},
		"./internal/config.Config":      {"./internal/config", "Config"},
		"example.com/app/config.Config": {"example.com/app/config", "Config"},
		"gopkg.in/x.v3/config.Settings": {"gopkg.in/x.v3/config", "Settings"},
		".Config":                       {".", "Config"},
	}
	for in, want := range cases {
		p, n, err := splitTypeFlag(in)
		if err != nil || p != want[0] || n != want[1] {
			t.Errorf("%s: %q %q %v, want %q %q", in, p, n, err, want[0], want[1])
		}
	}
	for _, bad := range []string{"", "./config.", "./config.Con-fig", "a/b.c/d"} {
		if _, _, err := splitTypeFlag(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestNearKey(t *testing.T) {
	s := &Shape{Kind: KStruct, Fields: []*Field{{Key: "max_open_conns"}, {Key: "backoff"}, {Key: "url"}}}
	cases := map[string]string{
		"maxOpenConns": "max_open_conns", "max-open-conns": "max_open_conns", "backof": "backoff",
		"bakcoff": "backoff", "uri": "url", "timeout": "", "u": "",
	}
	for in, want := range cases {
		got := ""
		if f := nearKey(in, s); f != nil {
			got = f.Key
		}
		if got != want {
			t.Errorf("nearKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--version"}, &out, &errb); code != 0 || out.String() != "straykeys 0.1.0\n" {
		t.Errorf("--version: %d %q", code, out.String())
	}
}
