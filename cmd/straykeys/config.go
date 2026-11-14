package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// configDoc is one config to check: a whole file, or the value of one key in
// a Kubernetes ConfigMap.
type configDoc struct {
	Name     string     // what to call it in messages
	File     string     // path for line references
	Root     *yaml.Node // the mapping to check
	LineBase int        // added to node lines (ConfigMap block values)
	Embedded bool       // lines come from a ConfigMap value
	JSONText bool       // the text is JSON, not YAML
	cutOff   bool       // YAML without a final line break
	lastLine int
	Extra    []*yaml.Node
}

// untrusted is an error about input that can't be checked.
type untrusted struct{ msg string }

func (u untrusted) Error() string { return u.msg }

func refuse(format string, a ...any) error { return untrusted{fmt.Sprintf(format, a...)} }

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, refuse("%s: %v", path, err)
	}
	if st.IsDir() {
		return nil, refuse("%s is a directory; pass config files", path)
	}
	return os.ReadFile(path)
}

func isJSONName(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".json")
}

// sniff refuses content that is plainly not a config file.
func sniff(name string, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return refuse("%s is empty", name)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return refuse("%s contains NUL bytes; it is not a text config file", name)
	}
	if !utf8.Valid(data) {
		return refuse("%s is not valid UTF-8 text", name)
	}
	trimmed := bytes.TrimSpace(data)
	lower := strings.ToLower(string(trimmed[:min(len(trimmed), 200)]))
	if strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html") || strings.HasPrefix(lower, "<?xml") {
		return refuse("%s is HTML or XML, not YAML or JSON", name)
	}
	return nil
}

// parseConfig parses YAML or JSON text into documents.
func parseConfig(name string, data []byte, jsonText bool) ([]*yaml.Node, error) {
	if err := sniff(name, data); err != nil {
		return nil, err
	}
	if jsonText {
		if !json.Valid(data) {
			var v any
			err := json.Unmarshal(data, &v)
			return nil, refuse("%s is not valid JSON (%v); a file cut off or with extra text fails here", name, err)
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, refuse("%s is not valid YAML: %v", name, err)
		}
		docs = append(docs, &n)
	}
	var out []*yaml.Node
	for _, d := range docs {
		if d.Kind == yaml.DocumentNode && len(d.Content) == 1 {
			out = append(out, d.Content[0])
		}
	}
	if len(out) == 0 {
		return nil, refuse("%s has no YAML document, only comments or separators", name)
	}
	return out, nil
}

// loadConfigs reads one file and returns the configs in it.
func loadConfigs(path, cmKey, at string) ([]*configDoc, error) {
	data, err := readInput(path)
	if err != nil {
		return nil, err
	}
	name := path
	if path == "-" {
		name = "stdin"
	}
	if cmKey != "" {
		return loadFromConfigMaps(name, data, cmKey, at)
	}
	docs, err := parseConfig(name, data, isJSONName(name))
	if err != nil {
		return nil, err
	}
	d := &configDoc{Name: name, File: name, Root: docs[0], Extra: docs[1:], JSONText: isJSONName(name)}
	d.cutOff = !d.JSONText && !bytes.HasSuffix(data, []byte("\n"))
	d.lastLine = bytes.Count(data, []byte("\n")) + 1
	if err := d.selectAt(at); err != nil {
		return nil, err
	}
	return []*configDoc{d}, nil
}

func loadFromConfigMaps(name string, data []byte, key, at string) ([]*configDoc, error) {
	docs, err := parseConfig(name, data, isJSONName(name))
	if err != nil {
		return nil, err
	}
	var items []*yaml.Node
	for _, d := range docs {
		if d.Kind != yaml.MappingNode {
			continue
		}
		if k := mapGet(d, "kind"); k != nil && k.Value == "List" {
			if list := mapGet(d, "items"); list != nil && list.Kind == yaml.SequenceNode {
				items = append(items, list.Content...)
			}
			continue
		}
		items = append(items, d)
	}
	var out []*configDoc
	configMaps := 0
	for _, d := range items {
		k := mapGet(d, "kind")
		if k == nil || k.Value != "ConfigMap" {
			continue
		}
		configMaps++
		cmName := "?"
		if md := mapGet(d, "metadata"); md != nil {
			if n := mapGet(md, "name"); n != nil {
				cmName = n.Value
			}
		}
		dataNode := mapGet(d, "data")
		if dataNode == nil {
			continue
		}
		v := mapGet(dataNode, key)
		if v == nil {
			continue
		}
		if v.Kind != yaml.ScalarNode {
			return nil, refuse("%s: ConfigMap %s key %s is not a string", name, cmName, key)
		}
		label := fmt.Sprintf("%s (ConfigMap %s, key %s)", name, cmName, key)
		inner, err := parseConfig(label, []byte(v.Value), isJSONName(key))
		if err != nil {
			return nil, err
		}
		base := v.Line
		if v.Style != yaml.LiteralStyle && v.Style != yaml.FoldedStyle {
			base = -1 // can't map inner lines to the file
		}
		cd := &configDoc{Name: label, File: name, Root: inner[0], Extra: inner[1:], LineBase: base, Embedded: true, JSONText: isJSONName(key)}
		if err := cd.selectAt(at); err != nil {
			return nil, err
		}
		out = append(out, cd)
	}
	if len(out) == 0 {
		if configMaps == 0 {
			return nil, refuse("%s has no ConfigMap, so --configmap-key %s has nothing to read", name, key)
		}
		return nil, refuse("%s has %d ConfigMap(s), and none has data key %s", name, configMaps, key)
	}
	return out, nil
}

// selectAt moves the root to a nested key, for configs that keep the
// program's settings under a top-level section.
func (d *configDoc) selectAt(at string) error {
	if at == "" {
		return nil
	}
	n := d.Root
	for _, part := range strings.Split(at, ".") {
		n = resolve(n)
		if n.Kind != yaml.MappingNode {
			return refuse("%s: --at %s: %s is not a mapping", d.Name, at, part)
		}
		v := mapGet(n, part)
		if v == nil {
			return refuse("%s has no key %s, so --at %s selects nothing", d.Name, part, at)
		}
		n = v
	}
	d.Root = n
	return nil
}

func resolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	m = resolve(m)
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return resolve(m.Content[i+1])
		}
	}
	return nil
}

func (d *configDoc) line(n *yaml.Node) int {
	if !d.Embedded {
		return n.Line
	}
	if d.LineBase < 0 {
		return 0
	}
	return d.LineBase + n.Line
}
