package decoders

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/knadh/koanf/v2"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
	sigsyaml "sigs.k8s.io/yaml"
)

var tool string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "straykeys-decoders")
	if err != nil {
		panic(err)
	}
	tool = filepath.Join(dir, "straykeys")
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", tool, "./cmd/straykeys")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building straykeys: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// decodeFunc decodes data with a real library into a fresh value of the type.
type decodeFunc func(data []byte) (any, error)

type mapProvider struct{ m map[string]any }

func (p mapProvider) ReadBytes() ([]byte, error)    { return nil, fmt.Errorf("not supported") }
func (p mapProvider) Read() (map[string]any, error) { return p.m, nil }

func yamlDecode[T any](data []byte) (any, error) {
	v := new(T)
	err := yaml.Unmarshal(data, v)
	return v, err
}

func jsonDecode[T any](data []byte) (any, error) {
	v := new(T)
	err := json.Unmarshal(data, v)
	return v, err
}

func sigsDecode[T any](data []byte) (any, error) {
	v := new(T)
	err := sigsyaml.Unmarshal(data, v)
	return v, err
}

func viperDecode[T any](data []byte) (any, error) {
	vp := viper.New()
	vp.SetConfigType("yaml")
	if err := vp.ReadConfig(bytes.NewReader(data)); err != nil {
		return nil, err
	}
	v := new(T)
	err := vp.Unmarshal(v)
	return v, err
}

func koanfDecode[T any](data []byte) (any, error) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	k := koanf.New(".")
	if err := k.Load(mapProvider{m}, nil); err != nil {
		return nil, err
	}
	v := new(T)
	err := k.Unmarshal("", v)
	return v, err
}

type suite struct {
	name    string
	typ     string
	decoder string
	fixture string
	asJSON  bool // write mutants as JSON
	decode  decodeFunc
	catches bool // the type keeps unknown keys, so none are ignored
}

var suites = []suite{
	{"yaml.v3", "YRoot", "yaml", "yroot.yaml", false, yamlDecode[YRoot], false},
	{"yaml.v3 inline map", "YCatch", "yaml", "ycatch.yaml", false, yamlDecode[YCatch], true},
	{"encoding/json", "JRoot", "json", "jroot.json", true, jsonDecode[JRoot], false},
	{"sigs.k8s.io/yaml", "JRoot", "json", "jroot.json", false, sigsDecode[JRoot], false},
	{"viper", "VRoot", "viper", "vroot.yaml", false, viperDecode[VRoot], false},
	{"viper remain", "VRemain", "viper", "vremain.yaml", false, viperDecode[VRemain], true},
	{"koanf", "KRoot", "koanf", "kroot.yaml", false, koanfDecode[KRoot], false},
	{"yaml.v3 anchors and merge keys", "YRoot", "yaml", "ymerge.yaml", false, yamlDecode[YRoot], false},
	{"viper anchors", "VRoot", "viper", "ymerge.yaml", false, viperDecode[VRoot], false},
	{"viper dotted keys", "VRoot", "viper", "vdotted.yaml", false, viperDecode[VRoot], false},
	{"koanf dotted keys", "KRoot", "koanf", "kdotted.yaml", false, koanfDecode[KRoot], false},
}

func parseNode(t *testing.T, data []byte) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc.Content[0]
}

func clearStyle(n *yaml.Node) {
	n.Style = 0
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
		n.Style = yaml.DoubleQuotedStyle
	}
	for _, c := range n.Content {
		clearStyle(c)
	}
}

// render writes the node as block YAML or indented JSON, one key per line.
func render(n *yaml.Node, asJSON bool) []byte {
	if !asJSON {
		clearStyle(n)
		out, err := yaml.Marshal(n)
		if err != nil {
			panic(err)
		}
		return out
	}
	var b strings.Builder
	var w func(n *yaml.Node, indent string)
	w = func(n *yaml.Node, indent string) {
		switch n.Kind {
		case yaml.MappingNode:
			b.WriteString("{\n")
			for i := 0; i < len(n.Content); i += 2 {
				b.WriteString(indent + "  " + strconv.Quote(n.Content[i].Value) + ": ")
				w(n.Content[i+1], indent+"  ")
				if i+2 < len(n.Content) {
					b.WriteString(",")
				}
				b.WriteString("\n")
			}
			b.WriteString(indent + "}")
		case yaml.SequenceNode:
			b.WriteString("[\n")
			for i, c := range n.Content {
				b.WriteString(indent + "  ")
				w(c, indent+"  ")
				if i+1 < len(n.Content) {
					b.WriteString(",")
				}
				b.WriteString("\n")
			}
			b.WriteString(indent + "]")
		default:
			switch n.ShortTag() {
			case "!!str":
				b.WriteString(strconv.Quote(n.Value))
			case "!!null":
				b.WriteString("null")
			default:
				b.WriteString(n.Value)
			}
		}
	}
	w(n, "")
	b.WriteString("\n")
	return []byte(b.String())
}

func copyNode(n *yaml.Node) *yaml.Node {
	c := *n
	c.Content = nil
	for _, k := range n.Content {
		c.Content = append(c.Content, copyNode(k))
	}
	return &c
}

func mappings(n *yaml.Node, out *[]*yaml.Node) {
	if n.Kind == yaml.MappingNode {
		*out = append(*out, n)
	}
	for _, c := range n.Content {
		mappings(c, out)
	}
}

func parentOf(root, target *yaml.Node) *yaml.Node {
	for i, c := range root.Content {
		if c == target {
			if root.Kind == yaml.MappingNode && i%2 == 1 {
				return root
			}
			if root.Kind == yaml.SequenceNode {
				return root
			}
		}
		if p := parentOf(c, target); p != nil {
			return p
		}
	}
	return nil
}

func hasFoldDup(m *yaml.Node) bool {
	seen := map[string]bool{}
	for i := 0; i < len(m.Content); i += 2 {
		k := strings.ToLower(m.Content[i].Value)
		if seen[k] {
			return true
		}
		seen[k] = true
	}
	return false
}

func anyFoldDup(n *yaml.Node) bool {
	if n.Kind == yaml.MappingNode && hasFoldDup(n) {
		return true
	}
	for _, c := range n.Content {
		if anyFoldDup(c) {
			return true
		}
	}
	return false
}

func camel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if 'A' <= r && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// mutate applies one random change to a key somewhere in the tree.
func mutate(r *rand.Rand, root *yaml.Node) {
	var ms []*yaml.Node
	mappings(root, &ms)
	m := ms[r.Intn(len(ms))]
	if len(m.Content) == 0 {
		return
	}
	i := 2 * r.Intn(len(m.Content)/2)
	k := m.Content[i]
	if r.Intn(4) == 0 {
		var ls []leaf
		leaves(root, nil, "", &ls)
		if len(ls) > 0 {
			l := ls[r.Intn(len(ls))].node
			v := values[r.Intn(len(values))]
			l.Tag, l.Value = v[0], v[1]
			return
		}
	}
	switch r.Intn(7) {
	case 0: // insert a letter
		p := r.Intn(len(k.Value) + 1)
		k.Value = k.Value[:p] + "x" + k.Value[p:]
	case 1: // drop a letter
		if len(k.Value) > 1 {
			p := r.Intn(len(k.Value))
			k.Value = k.Value[:p] + k.Value[p+1:]
		}
	case 2: // change case
		switch r.Intn(3) {
		case 0:
			k.Value = strings.ToUpper(k.Value[:1]) + k.Value[1:]
		case 1:
			k.Value = strings.ToUpper(k.Value)
		default:
			k.Value = strings.ToLower(k.Value)
		}
	case 3:
		k.Value = camel(k.Value)
	case 4:
		k.Value = snake(k.Value)
	case 5: // move the pair to the enclosing mapping, or into a nested one
		var target *yaml.Node
		if r.Intn(2) == 0 {
			p := parentOf(root, m)
			for p != nil && p.Kind != yaml.MappingNode {
				p = parentOf(root, p)
			}
			target = p
		} else {
			target = ms[r.Intn(len(ms))]
		}
		if target == nil || target == m {
			return
		}
		v := m.Content[i+1]
		if v == target || contains(v, target) {
			return
		}
		m.Content = append(m.Content[:i], m.Content[i+2:]...)
		target.Content = append(target.Content, k, v)
	case 6: // add a new key
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprintf("added_%d", r.Intn(1000))},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(r.Intn(100) + 1)})
	}
}

// values replace a scalar to test type rules: tag and text.
var values = [][2]string{
	{"!!str", "abc"}, {"!!int", "42"}, {"!!float", "1.5"}, {"!!bool", "true"}, {"!!str", "17"},
	{"!!str", ""}, {"!!str", "yes"}, {"!!int", "-3"}, {"!!str", "2m"}, {"!!int", "0"}, {"!!float", "2.0"},
	{"!!str", "true"}, {"!!int", "5000000000"},
}

func contains(n, target *yaml.Node) bool {
	for _, c := range n.Content {
		if c == target || contains(c, target) {
			return true
		}
	}
	return false
}

type leaf struct {
	node      *yaml.Node
	keyLines  []int // lines of the keys above it, innermost last
	pathLabel string
	anchored  bool // inside a node with an anchor
}

func leaves(n *yaml.Node, keys []int, path string, out *[]leaf) {
	leavesIn(n, keys, path, false, out)
}

func leavesIn(n *yaml.Node, keys []int, path string, anchored bool, out *[]leaf) {
	anchored = anchored || n.Anchor != ""
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			leavesIn(n.Content[i+1], append(append([]int{}, keys...), k.Line), path+"."+k.Value, anchored, out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			leavesIn(c, keys, fmt.Sprintf("%s[%d]", path, i), anchored, out)
		}
	case yaml.ScalarNode:
		if n.ShortTag() != "!!null" {
			*out = append(*out, leaf{n, keys, path, anchored})
		}
	}
}

func perturb(n *yaml.Node) {
	switch n.ShortTag() {
	case "!!int":
		v, _ := strconv.Atoi(n.Value)
		n.Value = strconv.Itoa(v + 1)
	case "!!float":
		v, _ := strconv.ParseFloat(n.Value, 64)
		n.Value = strconv.FormatFloat(v+1, 'g', -1, 64)
	case "!!bool":
		if n.Value == "true" {
			n.Value = "false"
		} else {
			n.Value = "true"
		}
	default:
		n.Value += "_zz"
	}
}

type toolReport struct {
	Configs  []string `json:"configs"`
	Findings []struct {
		Code   string `json:"code"`
		Config string `json:"config"`
		Line   int    `json:"line"`
		Key    string `json:"key"`
	} `json:"findings"`
}

func runTool(t *testing.T, s suite, files []string) toolReport {
	t.Helper()
	args := append([]string{"--dir", ".", "--type", s.typ, "--decoder", s.decoder, "--format", "json", "--fail-on", "never"}, files...)
	cmd := exec.Command(tool, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("straykeys %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	var rep toolReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("straykeys output: %v\n%s", err, out)
	}
	return rep
}

// TestMatchesRealDecoders makes many mutated configs per decoder and checks
// that the keys straykeys reports are exactly the keys the real library
// ignores. A key is ignored when changing its value doesn't change what the
// library decodes.
func TestMatchesRealDecoders(t *testing.T) {
	mutants := 120
	if testing.Short() {
		mutants = 30
	}
	if n, err := strconv.Atoi(os.Getenv("STRAYKEYS_MUTANTS")); err == nil {
		mutants = n
	}
	seed, _ := strconv.ParseInt(os.Getenv("STRAYKEYS_SEED"), 10, 64)
	for _, s := range suites {
		t.Run(s.name, func(t *testing.T) {
			base, err := os.ReadFile(filepath.Join("testdata", s.fixture))
			if err != nil {
				t.Fatal(err)
			}
			r := rand.New(rand.NewSource(int64(len(s.name))*7919 + seed))
			dir := t.TempDir()
			ext := ".yaml"
			if s.asJSON {
				ext = ".json"
			}
			baseRoot := parseNode(t, base)
			var files []string
			var datas [][]byte
			seen := map[string]bool{}
			for i := 0; len(files) < mutants && i < mutants*20; i++ {
				root := copyNode(parseNode(t, base))
				n := 1 + r.Intn(3)
				if i == 0 {
					n = 0 // the fixture itself
				}
				for j := 0; j < n; j++ {
					mutate(r, root)
				}
				if anyFoldDup(root) {
					continue
				}
				data := render(root, s.asJSON)
				if seen[string(data)] {
					continue
				}
				var check yaml.Node
				if yaml.Unmarshal(data, &check) != nil {
					continue // a move put an alias before its anchor
				}
				if !keepsTopKey(baseRoot, check.Content[0]) || (s.catches && !hasTopKey(check.Content[0], "name")) {
					continue // straykeys refuses a file with no known top-level key
				}
				seen[string(data)] = true
				f := filepath.Join(dir, fmt.Sprintf("m%03d%s", len(files), ext))
				if err := os.WriteFile(f, data, 0o644); err != nil {
					t.Fatal(err)
				}
				files = append(files, f)
				datas = append(datas, data)
			}
			rep := runTool(t, s, files)
			byFile := map[string][]int{}
			loadErrs := map[string][]string{}
			for _, f := range rep.Findings {
				switch f.Code {
				case "unknown-key", "wrong-case":
					byFile[f.Config] = append(byFile[f.Config], f.Line)
				case "wrong-shape", "duplicate-key", "alias-cycle":
					loadErrs[f.Config] = append(loadErrs[f.Config], f.Code+" "+f.Key)
				case "wrong-type":
					loadErrs[f.Config] = append(loadErrs[f.Config], f.Code+" "+f.Key)
				case "duration-without-unit":
					if s.decoder == "yaml" {
						loadErrs[f.Config] = append(loadErrs[f.Config], f.Code+" "+f.Key)
					}
				}
			}
			compared, ignoredSeen, failed := 0, 0, 0
			for i, f := range files {
				data := datas[i]
				full, err := s.decode(data)
				if err != nil {
					if len(loadErrs[f]) == 0 {
						t.Errorf("%s: the real decoder fails (%v), but straykeys reports no load error\n%s", filepath.Base(f), err, data)
					}
					failed++
					continue
				}
				if len(loadErrs[f]) > 0 {
					t.Errorf("%s: straykeys reports %v, but the real decoder loads it\n%s", filepath.Base(f), loadErrs[f], data)
					continue
				}
				reported := map[int]bool{}
				for _, l := range byFile[f] {
					reported[l] = true
				}
				root := parseNode(t, data)
				var ls []leaf
				leaves(root, nil, "", &ls)
				for _, l := range ls {
					if l.anchored {
						// Values under an anchor are used wherever the alias
						// is; straykeys doesn't report keys that only hold
						// an anchor, so these aren't compared.
						continue
					}
					// The value is ignored when no change to it changes what
					// the library decodes. Several changes are tried, because
					// weak typing maps some different values to one result.
					realIgnored := true
					origTag, origValue := l.node.Tag, l.node.Value
					for _, change := range []func(){
						func() { perturb(l.node) },
						func() { l.node.Tag, l.node.Value = "!!int", "0" },
						func() { l.node.Tag, l.node.Value = "!!bool", "false" },
						func() { l.node.Tag, l.node.Value = "!!str", "zz" },
					} {
						change()
						pdata := render(copyNode(root), s.asJSON)
						l.node.Tag, l.node.Value = origTag, origValue
						got, err := s.decode(pdata)
						if err != nil || !reflect.DeepEqual(full, got) {
							realIgnored = false
							break
						}
					}
					toolIgnored := false
					for _, kl := range l.keyLines {
						if reported[kl] {
							toolIgnored = true
						}
					}
					if realIgnored {
						ignoredSeen++
					}
					if realIgnored != toolIgnored {
						t.Errorf("%s line %d (%s): real decoder ignores it: %v, straykeys reports it: %v\n%s", filepath.Base(f), l.node.Line, l.pathLabel, realIgnored, toolIgnored, data)
					}
					compared++
				}
			}
			t.Logf("%d configs, %d values compared, %d ignored by the real decoder, %d configs the decoder refused", len(files), compared, ignoredSeen, failed)
			if ignoredSeen == 0 && !s.catches {
				t.Errorf("no mutant had an ignored key; the test is not testing anything")
			}
		})
	}
}

// TestBareDurations checks that a number for a time.Duration field is read
// as nanoseconds by the decoders straykeys says do that.
func TestBareDurations(t *testing.T) {
	v, err := viperDecode[VRoot]([]byte("name: a\ntimeout: 30\n"))
	if err != nil || v.(*VRoot).Timeout != 30 {
		t.Errorf("viper: %v %v", v, err)
	}
	k, err := koanfDecode[KRoot]([]byte("name: a\ntimeout: 30\n"))
	if err != nil || k.(*KRoot).Timeout != 30 {
		t.Errorf("koanf: %v %v", k, err)
	}
	j, err := jsonDecode[JRoot]([]byte(`{"name":"a","timeout":30}`))
	if err != nil || j.(*JRoot).Timeout != 30 {
		t.Errorf("json: %v %v", j, err)
	}
	if _, err := yamlDecode[YRoot]([]byte("name: a\ntimeout: 30\n")); err == nil {
		t.Errorf("yaml.v3 accepted a bare number for a duration")
	}
	files := map[string]string{"y.yaml": "name: a\ntimeout: 30\n", "j.json": `{"name":"a","timeout":30}` + "\n"}
	dir := t.TempDir()
	for name, body := range files {
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	for _, c := range []struct{ typ, dec, file string }{{"YRoot", "yaml", "y.yaml"}, {"VRoot", "viper", "y.yaml"}, {"KRoot", "koanf", "y.yaml"}, {"JRoot", "json", "j.json"}} {
		rep := runTool(t, suite{typ: c.typ, decoder: c.dec}, []string{filepath.Join(dir, c.file)})
		var codes []string
		for _, f := range rep.Findings {
			codes = append(codes, f.Code)
		}
		sort.Strings(codes)
		if strings.Join(codes, ",") != "duration-without-unit" {
			t.Errorf("%s: findings %v, want duration-without-unit", c.dec, codes)
		}
	}
}

// keepsTopKey reports whether the mutant still has one of the fixture's
// top-level keys.
func keepsTopKey(base, m *yaml.Node) bool {
	keys := map[string]bool{}
	for i := 0; i < len(base.Content); i += 2 {
		keys[base.Content[i].Value] = true
	}
	for i := 0; i < len(m.Content); i += 2 {
		if v := m.Content[i].Value; keys[v] && !strings.HasPrefix(v, "x_") && !strings.Contains(v, ".") {
			return true
		}
	}
	return false
}

// hasTopKey reports whether the mapping has the key. straykeys refuses a
// file whose only keys go to a catch-all map, so those mutants are skipped.
func hasTopKey(m *yaml.Node, key string) bool {
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return true
		}
	}
	return false
}
