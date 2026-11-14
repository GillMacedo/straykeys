package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Severity int

const (
	Warn Severity = iota + 1
	Error
)

func (s Severity) String() string {
	if s == Error {
		return "ERROR"
	}
	return "WARN"
}

// Finding is one problem in a config file.
type Finding struct {
	Severity Severity `json:"-"`
	Level    string   `json:"severity"`
	Code     string   `json:"code"`
	File     string   `json:"file"`
	Config   string   `json:"config"`
	Line     int      `json:"line,omitempty"`
	Key      string   `json:"key"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
}

type checker struct {
	dec      Decoder
	root     *Shape
	rootName string
	doc      *configDoc
	findings []Finding
	keys     int
	opaque   int
	topKnown int
	allowed  map[string]bool
	untyped  int // inside a promoted json field (see Field.Promoted)
	// expanding holds the anchors being expanded, to catch an alias that
	// refers to a node containing itself.
	expanding map[*yaml.Node]bool
	cycles    map[*yaml.Node]bool
}

func pathJoin(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func (c *checker) add(sev Severity, code string, n *yaml.Node, path, msg, fix string) {
	c.findings = append(c.findings, Finding{
		Severity: sev, Level: sev.String(), Code: code, File: c.doc.File, Config: c.doc.Name,
		Line: c.doc.line(n), Key: path, Message: msg, Fix: fix,
	})
}

func nodeKind(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		return fmt.Sprintf("the value %q", short(n.Value))
	}
	return "a value"
}

func short(s string) string {
	if len(s) > 40 {
		return s[:37] + "..."
	}
	return s
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

func (c *checker) checkDoc() {
	root := resolve(c.doc.Root)
	if root.Kind != yaml.MappingNode {
		return
	}
	if c.dec != DecYAML && !(c.dec == DecJSON && c.doc.JSONText) {
		// viper, koanf and sigs.k8s.io/yaml decode the whole document into
		// maps first, so a cycle anywhere stops them.
		c.findAliasCycles(root)
	}
	c.walk(root, c.root, "", true)
	if c.doc.cutOff {
		c.findings = append(c.findings, Finding{Severity: Error, Level: Error.String(), Code: "no-final-newline", File: c.doc.File, Config: c.doc.Name, Line: c.doc.lastLine,
			Message: "the file does not end with a line break, so it may have been cut off mid-line, and a cut value such as port: 80 becoming port: 8 still parses; this result can't show the file is complete",
			Fix:     "add a line break at the end of the file if it is complete"})
	}
	for i, extra := range c.doc.Extra {
		c.add(Error, "extra-document", extra, "", fmt.Sprintf("document %d after a --- separator is never read: decoders read only the first YAML document", i+2),
			"merge it into the first document, or split it into its own file")
	}
}

// walk checks node n against shape s at path.
func (c *checker) walk(n *yaml.Node, s *Shape, path string, top bool) {
	if n != nil && n.Kind == yaml.AliasNode {
		if !c.enterAlias(n, path) {
			return
		}
		defer delete(c.expanding, n.Alias)
	}
	n = resolve(n)
	if n == nil || isNull(n) {
		return
	}
	switch s.Kind {
	case KAny:
		if c.dec == DecYAML {
			c.findAliasCycles(n)
		}
		return
	case KOpaque:
		c.opaque++
		return
	case KStruct:
		if n.Kind != yaml.MappingNode {
			c.wrongShape(n, s, path, "a mapping of fields")
			return
		}
		c.walkStruct(n, s, path, top)
	case KMap:
		if n.Kind != yaml.MappingNode {
			c.wrongShape(n, s, path, "a mapping")
			return
		}
		pairs := c.pairs(n)
		c.duplicates(pairs, path, false)
		for _, p := range pairs {
			c.keys++
			c.walk(p.val, s.Elem, pathJoin(path, p.key.Value), false)
		}
	case KSlice:
		if n.Kind != yaml.SequenceNode {
			if c.dec.dotted() && n.Kind != yaml.SequenceNode {
				// mapstructure's weak typing turns one value into a list of one.
				c.walk(n, s.Elem, path+"[0]", false)
				return
			}
			c.wrongShape(n, s, path, "a list")
			return
		}
		for i, item := range n.Content {
			c.walk(item, s.Elem, fmt.Sprintf("%s[%d]", path, i), false)
		}
	case KScalar:
		if n.Kind != yaml.ScalarNode {
			c.wrongShape(n, s, path, "a single value")
			return
		}
		if s.Duration && c.checkDuration(n, path) {
			return
		}
		if msg := c.scalarProblem(n, s); msg != "" {
			c.add(Error, "wrong-type", n, path,
				fmt.Sprintf("%s is %s, but the field is %s; %s, so the program can't load this file", displayPath(path), nodeKind(n), s.TypeName, msg), "")
		}
	}
}

func (c *checker) wrongShape(n *yaml.Node, s *Shape, path, want string) {
	c.add(Error, "wrong-shape", n, path,
		fmt.Sprintf("%s is %s, but the field is %s, which needs %s; the decoder returns an error and the program can't load this file", displayPath(path), nodeKind(n), s.TypeName, want),
		"")
}

func displayPath(p string) string {
	if p == "" {
		return "the top level"
	}
	return p
}

// checkDuration reports a number given for a time.Duration field. It returns
// true when the value needs no further type check.
func (c *checker) checkDuration(n *yaml.Node, path string) bool {
	tag := n.ShortTag()
	if tag != "!!int" && tag != "!!float" {
		return false
	}
	if c.dec == DecJSON && tag == "!!float" {
		return false // encoding/json refuses a fraction for an int64
	}
	fix := fmt.Sprintf("write it with a unit, for example \"%ss\" or \"%sms\"", n.Value, n.Value)
	if c.dec == DecYAML {
		c.add(Error, "duration-without-unit", n, path,
			fmt.Sprintf("%s is the number %s with no unit, for a time.Duration field: yaml.v3 refuses to load it (yaml.v2 would read it as %s nanoseconds)", path, n.Value, n.Value), fix)
		return true
	}
	f, err := strconv.ParseFloat(n.Value, 64)
	if err != nil || f == 0 || f >= 1e6 || f <= -1e6 {
		// Zero is zero in any unit, and a million or more is most likely
		// meant as nanoseconds.
		return true
	}
	if c.dec == DecJSON {
		fix = fmt.Sprintf("encoding/json reads a time.Duration only as a number of nanoseconds: write %s000000000 for %s seconds, or use a field type that parses \"%ss\"", n.Value, n.Value, n.Value)
	}
	c.add(Error, "duration-without-unit", n, path,
		fmt.Sprintf("%s is the number %s with no unit, for a time.Duration field, so the program reads it as %s nanoseconds", path, n.Value, n.Value), fix)
	return true
}

type pair struct {
	key, val *yaml.Node
	merged   bool
}

// pairs lists a mapping's keys, expanding YAML merge keys (<<) after the
// mapping's own keys, which take precedence.
func (c *checker) pairs(n *yaml.Node) []pair {
	var own, merged []pair
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.Value == "<<" && k.ShortTag() == "!!merge" {
			var srcs []*yaml.Node
			if v.Kind == yaml.SequenceNode {
				srcs = v.Content
			} else {
				srcs = []*yaml.Node{v}
			}
			for _, src := range srcs {
				if src.Kind == yaml.AliasNode {
					if !c.enterAlias(src, "<<") {
						continue
					}
				}
				if m := resolve(src); m != nil && m.Kind == yaml.MappingNode {
					for _, p := range c.pairs(m) {
						p.merged = true
						merged = append(merged, p)
					}
				}
				if src.Kind == yaml.AliasNode {
					delete(c.expanding, src.Alias)
				}
			}
			continue
		}
		own = append(own, pair{key: k, val: v})
	}
	seen := map[string]bool{}
	for _, p := range own {
		seen[p.key.Value] = true
	}
	for _, p := range merged {
		if !seen[p.key.Value] {
			seen[p.key.Value] = true
			own = append(own, p)
		}
	}
	return own
}

// duplicates reports keys that appear twice in one mapping. YAML decoders
// refuse them; encoding/json keeps the last one without a word.
func (c *checker) duplicates(pairs []pair, path string, fold bool) {
	first := map[string]pair{}
	for _, p := range pairs {
		if p.merged || p.key.Kind != yaml.ScalarNode {
			continue
		}
		k := p.key.Value
		if fold {
			k = strings.ToLower(k)
		}
		if prev, ok := first[k]; ok {
			var msg string
			if c.dec == DecJSON && prev.key.Value == p.key.Value {
				msg = fmt.Sprintf("%s appears twice in this mapping (first on line %d); encoding/json keeps the last value and ignores the first without an error", pathJoin(path, p.key.Value), c.doc.line(prev.key))
			} else if prev.key.Value == p.key.Value {
				msg = fmt.Sprintf("%s appears twice in this mapping (first on line %d); YAML decoders refuse duplicate keys, so the program can't load this file", pathJoin(path, p.key.Value), c.doc.line(prev.key))
			} else {
				msg = fmt.Sprintf("%s and %s (line %d) fill the same field; only one of the values is used", pathJoin(path, p.key.Value), prev.key.Value, c.doc.line(prev.key))
			}
			c.add(Error, "duplicate-key", p.key, pathJoin(path, p.key.Value), msg, "keep one of them")
			continue
		}
		first[k] = p
	}
}

func (c *checker) walkStruct(n *yaml.Node, s *Shape, path string, top bool) {
	pairs := c.pairs(n)
	c.duplicates(pairs, path, false)
	used := map[*Field]*yaml.Node{}
	for _, p := range pairs {
		if p.key.Kind != yaml.ScalarNode {
			c.add(Error, "unknown-key", p.key, path, fmt.Sprintf("%s has a key that is not a plain string, which no field can match", displayPath(path)), "")
			continue
		}
		key := p.key.Value
		c.keys++
		segs := []string{key}
		if c.dec.dotted() && strings.Contains(key, ".") {
			segs = strings.Split(key, ".")
		}
		c.walkKey(p, segs, s, path, top, len(segs) == 1, used)
	}
}

// walkKey matches a key (split on dots for viper and koanf) to a field.
// whole is true when the key in the file had no dots.
func (c *checker) walkKey(p pair, segs []string, s *Shape, path string, top, whole bool, used map[*Field]*yaml.Node) {
	key := segs[0]
	full := pathJoin(path, key)
	if c.allowed[strings.ToLower(full)] {
		if top {
			c.topKnown++
		}
		return
	}
	f, folded := s.lookup(c.dec, key)
	if f != nil && folded && !c.dec.caseInsensitive() && s.CatchAll != nil {
		f = nil // yaml puts the key in the ,inline map
	}
	if f != nil && folded && !c.dec.caseInsensitive() {
		c.add(Error, "wrong-case", p.key, full,
			fmt.Sprintf("%s does not match field %s, whose yaml key is %q: yaml matches keys exactly, so this value is ignored", full, f.GoName, f.Key),
			fmt.Sprintf("rename the key to %s", f.Key))
		if top {
			c.topKnown++
		}
		return
	}
	if f == nil {
		// Keys taken by a catch-all map, and keys that only hold an anchor,
		// don't count toward topKnown: a file made only of them could be
		// any text that happens to parse as a mapping.
		if s.CatchAll != nil {
			c.descend(p, segs[1:], s.CatchAll, full)
			return
		}
		if resolve(p.val) != nil && resolve(p.val).Anchor != "" && p.val.Kind != yaml.AliasNode {
			// A key that only holds an anchor for use elsewhere, such as
			// x-defaults: &defaults. Its values are checked where used.
			return
		}
		c.unknown(p, key, s, path)
		return
	}
	if top {
		c.topKnown++
	}
	if whole {
		if prev, ok := used[f]; ok && prev.Value != p.key.Value {
			c.add(Error, "duplicate-key", p.key, full,
				fmt.Sprintf("%s and %s (line %d) both fill field %s; only one of the values is used", full, pathJoin(path, prev.Value), c.doc.line(prev), f.GoName),
				"keep one of them")
			return
		}
		used[f] = p.key
	}
	if f.Promoted {
		c.untyped++
		defer func() { c.untyped-- }()
	}
	c.descend(p, segs[1:], f.Shape, pathJoin(path, f.Key))
}

// descend continues a dotted key into shape s, or checks the value when no
// segments are left.
func (c *checker) descend(p pair, segs []string, s *Shape, path string) {
	if len(segs) == 0 {
		c.walk(p.val, s, path, false)
		return
	}
	switch s.Kind {
	case KStruct:
		c.walkKey(p, segs, s, path, false, false, map[*Field]*yaml.Node{})
	case KMap:
		c.descend(p, segs[1:], s.Elem, pathJoin(path, segs[0]))
	case KAny, KOpaque:
	default:
		c.add(Error, "wrong-shape", p.key, path,
			fmt.Sprintf("%s goes below %s, but the field is %s, which can't hold keys; the decoder returns an error and the program can't load this file", p.key.Value, displayPath(path), s.TypeName), "")
	}
}

func (c *checker) unknown(p pair, key string, s *Shape, path string) {
	full := pathJoin(path, key)
	msg := fmt.Sprintf("%s is not a field of %s, so the program ignores it", full, s.TypeName)
	var fix string
	if f := nearKey(key, s); f != nil {
		fix = fmt.Sprintf("did you mean %s? (field %s)", pathJoin(path, f.Key), f.GoName)
	} else if where := c.elsewhere(key, path, s); where != "" {
		fix = fmt.Sprintf("%s is a key of %s; move it there", key, where)
	} else {
		fix = fmt.Sprintf("remove it, or add a field for it; %s accepts: %s", s.TypeName, keyList(s))
	}
	c.add(Error, "unknown-key", p.key, full, msg, fix)
}

func keyList(s *Shape) string {
	var ks []string
	for _, f := range s.Fields {
		ks = append(ks, f.Key)
	}
	if len(ks) == 0 {
		return "no keys"
	}
	if len(ks) > 12 {
		return strings.Join(ks[:12], ", ") + fmt.Sprintf(" and %d more", len(ks)-12)
	}
	return strings.Join(ks, ", ")
}

// norm folds case and drops separators, so max_conns, maxConns and
// max-conns compare equal.
func norm(s string) string {
	s = strings.ToLower(s)
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(s)
}

func nearKey(key string, s *Shape) *Field {
	nk := norm(key)
	for _, f := range s.Fields {
		if norm(f.Key) == nk {
			return f
		}
	}
	var best *Field
	bestD := 99
	limit := 2
	if len(nk) <= 4 {
		limit = 1
	}
	for _, f := range s.Fields {
		d := editDistance(nk, norm(f.Key))
		if d <= limit && d < bestD {
			best, bestD = f, d
		}
	}
	return best
}

// elsewhere finds other places in the schema that accept key: first below
// the struct the key is in (a missing level such as client:), then anywhere.
func (c *checker) elsewhere(key, path string, here *Shape) string {
	match := func(f *Field) bool {
		return f.Key == key || (c.dec.caseInsensitive() && strings.EqualFold(f.Key, key))
	}
	var found []string
	var below func(s *Shape, p string, depth int, onPath map[*Shape]bool)
	below = func(s *Shape, p string, depth int, onPath map[*Shape]bool) {
		if s == nil || onPath[s] || depth > 3 || len(found) >= 3 {
			return
		}
		onPath[s] = true
		defer delete(onPath, s)
		switch s.Kind {
		case KStruct:
			if depth > 0 {
				for _, f := range s.Fields {
					if match(f) {
						found = append(found, displayPath(p))
					}
				}
			}
			for _, f := range s.Fields {
				below(f.Shape, pathJoin(p, f.Key), depth+1, onPath)
			}
		case KSlice:
			below(s.Elem, p+"[]", depth+1, onPath)
		}
	}
	below(here, path, 0, map[*Shape]bool{})
	if len(found) > 0 {
		return strings.Join(found, ", ")
	}
	seen := map[*Shape]bool{}
	var walk func(s *Shape, p string, depth int)
	walk = func(s *Shape, p string, depth int) {
		if s == nil || seen[s] || depth > 6 {
			return
		}
		seen[s] = true
		switch s.Kind {
		case KStruct:
			for _, f := range s.Fields {
				if match(f) && p != path {
					found = append(found, displayPath(p))
				}
			}
			for _, f := range s.Fields {
				walk(f.Shape, pathJoin(p, f.Key), depth+1)
			}
		case KSlice:
			walk(s.Elem, p+"[]", depth+1)
		case KMap:
			walk(s.Elem, pathJoin(p, "*"), depth+1)
		}
	}
	walk(c.root, "", 0)
	sort.SliceStable(found, func(i, j int) bool { return commonPrefix(found[i], path) > commonPrefix(found[j], path) })
	if len(found) > 3 {
		found = found[:3]
	}
	return strings.Join(found, ", ")
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// yaml11Bools are the YAML 1.1 words yaml.v3 still accepts for a bool field.
var yaml11Bools = map[string]bool{
	"y": true, "Y": true, "yes": true, "Yes": true, "YES": true, "on": true, "On": true, "ON": true,
	"n": true, "N": true, "no": true, "No": true, "NO": true, "off": true, "Off": true, "OFF": true,
}

// scalarProblem says why the decoder would refuse scalar n for shape s, or
// returns "" when it accepts it.
func (c *checker) scalarProblem(n *yaml.Node, s *Shape) string {
	tag := n.ShortTag()
	if tag == "!!null" {
		return ""
	}
	plain11 := n.Style == 0 && tag == "!!str" && yaml11Bools[n.Value]
	switch c.dec {
	case DecJSON:
		if !c.doc.JSONText && plain11 {
			// sigs.k8s.io/yaml parses YAML 1.1, where a plain yes or off is a bool.
			tag = "!!bool"
		}
		if c.doc.JSONText {
			return jsonScalarProblem(tag, n.Value, s)
		}
		if c.untyped > 0 {
			return sigsUntypedProblem(tag, n.Value, s)
		}
		return sigsScalarProblem(tag, n.Value, s)
	case DecYAML:
		return yamlScalarProblem(tag, n.Value, s)
	default:
		return weakScalarProblem(tag, n.Value, s, string(c.dec))
	}
}

func jsonScalarProblem(tag, v string, s *Shape) string {
	const refuses = "encoding/json refuses it"
	if s.Quoted {
		return quotedProblem(tag, v, s, refuses)
	}
	if s.Bytes {
		if tag != "!!str" {
			return refuses
		}
		return ""
	}
	switch s.Scalar {
	case SString:
		if tag != "!!str" {
			return refuses
		}
	case SBool:
		if tag != "!!bool" {
			return refuses
		}
	case SInt, SUint:
		if tag != "!!int" || (s.Scalar == SUint && strings.HasPrefix(v, "-")) {
			return refuses
		}
	case SFloat:
		if tag != "!!int" && tag != "!!float" {
			return refuses
		}
	}
	return ""
}

// sigsScalarProblem follows sigs.k8s.io/yaml, which converts YAML to JSON
// using the target type, so any scalar can fill a string field.
func sigsScalarProblem(tag, v string, s *Shape) string {
	const refuses = "the YAML-to-JSON decoder (sigs.k8s.io/yaml) refuses it"
	if s.Quoted {
		return quotedProblem(tag, v, s, refuses)
	}
	if s.Bytes || s.Scalar == SString {
		return ""
	}
	switch s.Scalar {
	case SBool:
		if tag != "!!bool" {
			return refuses
		}
	case SInt, SUint, SFloat:
		if tag == "!!str" || tag == "!!bool" {
			return refuses
		}
		if s.Scalar != SFloat && tag == "!!float" && !integral(v) {
			return refuses
		}
		if s.Scalar == SUint && strings.HasPrefix(v, "-") {
			return refuses
		}
	}
	return ""
}

// sigsUntypedProblem applies when sigs.k8s.io/yaml has lost the target type
// (below a promoted field): values reach encoding/json as YAML typed them.
func sigsUntypedProblem(tag, v string, s *Shape) string {
	const refuses = "the YAML-to-JSON decoder (sigs.k8s.io/yaml) refuses it: below a field promoted from an embedded struct it doesn't convert values to the field's type"
	if s.Scalar == SString || s.Bytes {
		if tag != "!!str" {
			return refuses
		}
		return ""
	}
	if p := sigsScalarProblem(tag, v, s); p != "" {
		return p
	}
	return ""
}

func integral(v string) bool {
	f, err := strconv.ParseFloat(v, 64)
	return err == nil && f == float64(int64(f))
}

func yamlScalarProblem(tag, v string, s *Shape) string {
	const refuses = "yaml.v3 refuses it"
	if s.Duration {
		if tag == "!!bool" {
			return refuses
		}
		if tag == "!!str" {
			if _, err := time.ParseDuration(v); err != nil {
				return "it is not a duration such as 30s or 1m30s; " + refuses
			}
		}
		return ""
	}
	if s.Bytes {
		return ""
	}
	switch s.Scalar {
	case SBool:
		if tag != "!!bool" && !yaml11Bools[v] {
			return refuses
		}
	case SInt, SUint:
		if tag != "!!int" && tag != "!!float" {
			return refuses
		}
		if s.Scalar == SUint && strings.HasPrefix(v, "-") {
			return refuses
		}
	case SFloat:
		if tag != "!!int" && tag != "!!float" {
			return refuses
		}
	}
	return ""
}

// weakScalarProblem follows mapstructure with WeaklyTypedInput, as viper and
// koanf configure it: strings are parsed into numbers and bools.
func weakScalarProblem(tag, v string, s *Shape, dec string) string {
	refuses := dec + " refuses it"
	if s.Duration {
		if tag == "!!str" {
			if _, err := time.ParseDuration(v); err != nil {
				return "it is not a duration such as 30s or 1m30s; " + refuses
			}
		}
		return ""
	}
	if tag != "!!str" || s.Bytes {
		return ""
	}
	switch s.Scalar {
	case SBool:
		if v == "" {
			return ""
		}
		if _, err := strconv.ParseBool(v); err != nil {
			return refuses
		}
	case SInt:
		if v == "" {
			return ""
		}
		if _, err := strconv.ParseInt(v, 0, 64); err != nil {
			return refuses
		}
	case SUint:
		if v == "" {
			return ""
		}
		if _, err := strconv.ParseUint(v, 0, 64); err != nil {
			return refuses
		}
	case SFloat:
		if v == "" {
			return ""
		}
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return refuses
		}
	}
	return ""
}

// quotedProblem checks a field with the json ",string" option: the value
// must be a JSON string that holds the field's scalar.
func quotedProblem(tag, v string, s *Shape, refuses string) string {
	if tag != "!!str" {
		return "the field has the json \",string\" option, so it must be a quoted string; " + refuses
	}
	var err error
	switch s.Scalar {
	case SBool:
		if v != "true" && v != "false" {
			err = strconv.ErrSyntax
		}
	case SInt:
		_, err = strconv.ParseInt(v, 10, 64)
	case SUint:
		_, err = strconv.ParseUint(v, 10, 64)
	case SFloat:
		_, err = strconv.ParseFloat(v, 64)
	case SString:
		_, err = strconv.Unquote(v)
		if !strings.HasPrefix(v, "\"") {
			err = strconv.ErrSyntax
		}
	}
	if err != nil {
		return "the field has the json \",string\" option, and the text in the quotes is not its type; " + refuses
	}
	return ""
}

// enterAlias marks an alias's anchor as being expanded. It reports false,
// with a finding, when the anchor is already being expanded: the alias is
// inside the node it refers to.
func (c *checker) enterAlias(n *yaml.Node, path string) bool {
	if c.expanding == nil {
		c.expanding = map[*yaml.Node]bool{}
	}
	if n.Alias == nil {
		return false
	}
	if c.expanding[n.Alias] {
		if c.dec == DecYAML {
			// yaml.v3 fails only on a cycle it decodes.
			c.reportCycle(n, path)
		}
		return false
	}
	c.expanding[n.Alias] = true
	return true
}

// findAliasCycles reports aliases that sit inside the node they refer to.
// It looks at the whole document, including keys that only hold anchors.
func (c *checker) findAliasCycles(root *yaml.Node) {
	onPath := map[*yaml.Node]bool{}
	done := map[*yaml.Node]bool{}
	var visit func(n *yaml.Node)
	visit = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.AliasNode {
			if n.Alias != nil && onPath[n.Alias] {
				c.reportCycle(n, "")
				return
			}
			visit(n.Alias)
			return
		}
		if done[n] || onPath[n] {
			return
		}
		onPath[n] = true
		for _, k := range n.Content {
			visit(k)
		}
		onPath[n] = false
		done[n] = true
	}
	visit(root)
}

func (c *checker) reportCycle(n *yaml.Node, path string) {
	if c.cycles == nil {
		c.cycles = map[*yaml.Node]bool{}
	}
	if c.cycles[n] {
		return
	}
	c.cycles[n] = true
	c.add(Error, "alias-cycle", n, path,
		fmt.Sprintf("the alias *%s is inside the node it refers to; the decoder refuses this, so the program can't load this file", n.Value), "")
}
