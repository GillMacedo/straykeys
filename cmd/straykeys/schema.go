package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"
)

// Decoder names the library that turns the config file into the struct.
// Each has its own rules for which key fills which field.
type Decoder string

const (
	DecYAML  Decoder = "yaml"  // gopkg.in/yaml.v3, go.yaml.in/yaml/v3 (and v2's key rules)
	DecJSON  Decoder = "json"  // encoding/json, sigs.k8s.io/yaml, github.com/ghodss/yaml
	DecViper Decoder = "viper" // github.com/spf13/viper Unmarshal (mapstructure tags)
	DecKoanf Decoder = "koanf" // github.com/knadh/koanf Unmarshal (koanf tags)
)

var decoders = []Decoder{DecYAML, DecJSON, DecViper, DecKoanf}

func (d Decoder) tag() string {
	switch d {
	case DecYAML:
		return "yaml"
	case DecJSON:
		return "json"
	case DecViper:
		return "mapstructure"
	case DecKoanf:
		return "koanf"
	}
	return ""
}

// caseInsensitive reports whether the decoder falls back to a case-insensitive
// match when no key matches exactly.
func (d Decoder) caseInsensitive() bool { return d != DecYAML }

// dotted reports whether a key containing dots is split into nested keys.
func (d Decoder) dotted() bool { return d == DecViper }

type Kind int

const (
	KStruct Kind = iota
	KMap
	KSlice
	KScalar
	KAny    // interface{}: anything goes
	KOpaque // the type decodes itself (UnmarshalYAML, UnmarshalJSON, UnmarshalText)
)

// Shape is what the decoder expects at one place in the config.
type Shape struct {
	Kind     Kind
	TypeName string // Go type, for messages
	Pos      token.Position
	Fields   []*Field // KStruct
	CatchAll *Shape   // KStruct: ",inline" map or ",remain" field takes unknown keys
	Elem     *Shape   // KMap, KSlice
	Duration bool     // KScalar: time.Duration
	Bytes    bool     // KScalar: []byte
	Scalar   ScalarKind
	Quoted   bool // json ",string": the value is a JSON string holding the scalar
}

// ScalarKind is the Go kind of a scalar field.
type ScalarKind int

const (
	SOther ScalarKind = iota
	SString
	SBool
	SInt
	SUint
	SFloat
)

// Field is one key a struct accepts.
type Field struct {
	Key    string
	GoName string // dotted path of Go field names, for messages
	Shape  *Shape
	Pos    token.Position
	// Promoted is true for a json field promoted from an embedded struct.
	// sigs.k8s.io/yaml then loses track of the type below it.
	Promoted bool
}

type schemaBuilder struct {
	dec   Decoder
	fset  *token.FileSet
	decls map[token.Pos]*ast.FuncDecl // methods by name position
	plain int                         // types that decode through a plain copy
	memo  map[*types.Named]*Shape
	stack map[*types.Named]bool
	err   error
}

func buildSchema(dec Decoder, l *loader, tn *types.TypeName) (*Shape, int, error) {
	b := &schemaBuilder{dec: dec, fset: l.fset, memo: map[*types.Named]*Shape{}, stack: map[*types.Named]bool{}, decls: map[token.Pos]*ast.FuncDecl{}}
	for _, files := range l.files {
		for _, f := range files {
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil && fd.Body != nil {
					b.decls[fd.Name.Pos()] = fd
				}
			}
		}
	}
	t := tn.Type()
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	s := b.shape(t)
	if b.err != nil {
		return nil, 0, b.err
	}
	if s.Kind == KOpaque {
		return nil, 0, fmt.Errorf("%s decodes itself (it has an UnmarshalYAML, UnmarshalJSON or UnmarshalText method that doesn't just decode a plain copy of the struct), so its keys can't be known from its fields; name the struct that method decodes into", tn.Name())
	}
	return s, b.plain, nil
}

// methodNamed returns the method of *t with the given name.
func methodNamed(t types.Type, name string) *types.Func {
	ms := types.NewMethodSet(types.NewPointer(t))
	for i := 0; i < ms.Len(); i++ {
		if f, ok := ms.At(i).Obj().(*types.Func); ok && f.Name() == name {
			return f
		}
	}
	return nil
}

// decodesPlainCopy recognizes the common pattern of an Unmarshal method that
// sets defaults and decodes into a copy of the type without its methods:
//
//	func (c *Config) UnmarshalYAML(unmarshal func(any) error) error {
//		*c = DefaultConfig
//		type plain Config
//		return unmarshal((*plain)(c))
//	}
//
// The keys are then the fields of Config.
func (b *schemaBuilder) decodesPlainCopy(t types.Type, method string) bool {
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	m := methodNamed(t, method)
	if m == nil {
		return false
	}
	fd := b.decls[m.Pos()]
	if fd == nil {
		return false
	}
	plainNames := map[string]bool{}
	ast.Inspect(fd.Body, func(x ast.Node) bool {
		ts, ok := x.(*ast.TypeSpec)
		if !ok {
			return true
		}
		if id, ok := ts.Type.(*ast.Ident); ok && id.Name == n.Obj().Name() {
			plainNames[ts.Name.Name] = true
		}
		return true
	})
	if len(plainNames) == 0 {
		return false
	}
	found := false
	ast.Inspect(fd.Body, func(x ast.Node) bool {
		call, ok := x.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		paren, ok := call.Fun.(*ast.ParenExpr)
		if !ok {
			return true
		}
		star, ok := paren.X.(*ast.StarExpr)
		if !ok {
			return true
		}
		if id, ok := star.X.(*ast.Ident); ok && plainNames[id.Name] {
			found = true
		}
		return true
	})
	return found
}

func typeString(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}

func hasMethod(t types.Type, name string) bool {
	ms := types.NewMethodSet(types.NewPointer(t))
	for i := 0; i < ms.Len(); i++ {
		if ms.At(i).Obj().Name() == name {
			return true
		}
	}
	return false
}

func isDuration(t types.Type) bool {
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	o := n.Obj()
	return o.Pkg() != nil && o.Pkg().Path() == "time" && o.Name() == "Duration"
}

// decodesItself reports whether the decoder hands the value to the type's own
// method, so its contents can't be checked from the struct.
func (b *schemaBuilder) decodesItself(t types.Type) bool {
	if _, ok := t.Underlying().(*types.Interface); ok {
		return false
	}
	switch b.dec {
	case DecYAML:
		if hasMethod(t, "UnmarshalYAML") {
			if b.decodesPlainCopy(t, "UnmarshalYAML") {
				b.plain++
				return false
			}
			return true
		}
		return hasMethod(t, "UnmarshalText")
	case DecJSON:
		if hasMethod(t, "UnmarshalJSON") {
			if b.decodesPlainCopy(t, "UnmarshalJSON") {
				b.plain++
				return false
			}
			return true
		}
		return hasMethod(t, "UnmarshalText")
	default:
		// viper users often add a TextUnmarshaler hook, and koanf has one by
		// default, so treat these types as decoding themselves.
		return hasMethod(t, "UnmarshalText")
	}
}

func (b *schemaBuilder) shape(t types.Type) *Shape {
	if b.err != nil {
		return &Shape{Kind: KAny}
	}
	if n, ok := t.(*types.Named); ok {
		if s, ok := b.memo[n]; ok {
			return s
		}
		s := &Shape{TypeName: typeString(t), Pos: b.fset.Position(n.Obj().Pos())}
		b.memo[n] = s
		b.fill(s, t)
		return s
	}
	if a, ok := t.(*types.Alias); ok {
		return b.shape(types.Unalias(a))
	}
	s := &Shape{TypeName: typeString(t)}
	b.fill(s, t)
	return s
}

func (b *schemaBuilder) fill(s *Shape, t types.Type) {
	if isDuration(t) {
		s.Kind, s.Duration, s.Scalar = KScalar, true, SInt
		return
	}
	if b.decodesItself(t) {
		s.Kind = KOpaque
		return
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		if u.Kind() == types.Invalid {
			b.err = fmt.Errorf("type %s could not be resolved, so its fields can't be checked", typeString(t))
		}
		s.Kind = KScalar
		switch info := u.Info(); {
		case info&types.IsString != 0:
			s.Scalar = SString
		case info&types.IsBoolean != 0:
			s.Scalar = SBool
		case info&types.IsUnsigned != 0:
			s.Scalar = SUint
		case info&types.IsInteger != 0:
			s.Scalar = SInt
		case info&types.IsFloat != 0:
			s.Scalar = SFloat
		}
	case *types.Pointer:
		inner := b.shape(u.Elem())
		*s = *inner
	case *types.Struct:
		s.Kind = KStruct
		b.structFields(s, u, t)
	case *types.Map:
		s.Kind = KMap
		s.Elem = b.shape(u.Elem())
	case *types.Slice:
		if e, ok := u.Elem().Underlying().(*types.Basic); ok && e.Kind() == types.Byte {
			s.Kind, s.Bytes = KScalar, true
			return
		}
		s.Kind = KSlice
		s.Elem = b.shape(u.Elem())
	case *types.Array:
		s.Kind = KSlice
		s.Elem = b.shape(u.Elem())
	case *types.Interface:
		s.Kind = KAny
	case *types.TypeParam:
		s.Kind = KAny
	default:
		// chan, func: no decoder fills these from a file.
		s.Kind = KOpaque
	}
}

type rawField struct {
	v      *types.Var
	tag    string
	depth  int
	path   string
	quoted bool // json ",string"
}

func derefStruct(t types.Type) (types.Type, *types.Struct) {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	st, _ := t.Underlying().(*types.Struct)
	return t, st
}

func (b *schemaBuilder) addField(s *Shape, key, goName string, v *types.Var) {
	s.Fields = append(s.Fields, &Field{Key: key, GoName: goName, Shape: b.shape(v.Type()), Pos: b.fset.Position(v.Pos())})
}

func (b *schemaBuilder) structFields(s *Shape, st *types.Struct, t types.Type) {
	switch b.dec {
	case DecYAML:
		b.yamlFields(s, st, "", map[string]bool{})
	case DecJSON:
		b.jsonFields(s, st)
	default:
		b.mapstructureFields(s, st, "")
	}
}

// yamlFields follows getStructInfo in gopkg.in/yaml.v3.
func (b *schemaBuilder) yamlFields(s *Shape, st *types.Struct, prefix string, seen map[string]bool) {
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		raw := st.Tag(i)
		if !f.Exported() && !f.Embedded() {
			continue
		}
		tag := reflect.StructTag(raw).Get("yaml")
		if tag == "" && !strings.Contains(raw, ":") {
			tag = raw
		}
		if tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		inline := false
		for _, flag := range parts[1:] {
			switch flag {
			case "omitempty", "flow":
			case "inline":
				inline = true
			default:
				b.err = fmt.Errorf("field %s of %s has yaml flag %q, which yaml.v3 rejects when decoding", f.Name(), s.TypeName, flag)
				return
			}
		}
		name := parts[0]
		goName := prefix + f.Name()
		if inline {
			switch u := f.Type().Underlying().(type) {
			case *types.Map:
				if k, ok := u.Key().Underlying().(*types.Basic); !ok || k.Kind() != types.String {
					b.err = fmt.Errorf("field %s of %s: yaml ,inline needs a map with string keys", f.Name(), s.TypeName)
					return
				}
				s.CatchAll = b.shape(u.Elem())
			default:
				et, est := derefStruct(f.Type())
				if est == nil {
					b.err = fmt.Errorf("field %s of %s: yaml ,inline may only be used on a struct or map field", f.Name(), s.TypeName)
					return
				}
				if hasMethod(et, "UnmarshalYAML") {
					// The embedded type reads the whole mapping itself.
					s.CatchAll = &Shape{Kind: KOpaque}
					continue
				}
				b.yamlFields(s, est, goName+".", seen)
			}
			continue
		}
		if !f.Exported() {
			continue
		}
		key := name
		if key == "" {
			key = strings.ToLower(f.Name())
		}
		if seen[key] {
			b.err = fmt.Errorf("%s has two fields with yaml key %q, which yaml.v3 rejects when decoding", s.TypeName, key)
			return
		}
		seen[key] = true
		b.addField(s, key, goName, f)
	}
}

// jsonFields follows typeFields in encoding/json: embedded structs without a
// name tag are promoted, the shallowest field wins, and at equal depth a
// tagged field beats untagged ones.
func (b *schemaBuilder) jsonFields(s *Shape, st *types.Struct) {
	type level struct {
		st   *types.Struct
		path string
	}
	current := []level{{st, ""}}
	var all []rawField
	visited := map[*types.Struct]bool{}
	for depth := 0; len(current) > 0; depth++ {
		var next []level
		for _, lv := range current {
			if visited[lv.st] {
				continue
			}
			visited[lv.st] = true
			for i := 0; i < lv.st.NumFields(); i++ {
				f := lv.st.Field(i)
				if f.Embedded() {
					_, est := derefStruct(f.Type())
					if !f.Exported() && est == nil {
						continue
					}
				} else if !f.Exported() {
					continue
				}
				tag := reflect.StructTag(lv.st.Tag(i)).Get("json")
				if tag == "-" {
					continue
				}
				tagParts := strings.Split(tag, ",")
				name := tagParts[0]
				quoted := false
				for _, o := range tagParts[1:] {
					if o == "string" {
						quoted = true
					}
				}
				if !validJSONTag(name) {
					name = ""
				}
				if name == "" && f.Embedded() {
					if _, est := derefStruct(f.Type()); est != nil {
						next = append(next, level{est, lv.path + f.Name() + "."})
						continue
					}
				}
				if !f.Exported() {
					continue
				}
				all = append(all, rawField{v: f, tag: name, depth: depth, path: lv.path + f.Name(), quoted: quoted})
			}
		}
		current = next
	}
	byName := map[string][]rawField{}
	var order []string
	for _, rf := range all {
		n := rf.tag
		if n == "" {
			n = rf.v.Name()
		}
		if _, ok := byName[n]; !ok {
			order = append(order, n)
		}
		byName[n] = append(byName[n], rf)
	}
	for _, n := range order {
		group := byName[n]
		min := group[0].depth
		for _, rf := range group {
			if rf.depth < min {
				min = rf.depth
			}
		}
		var top, tagged []rawField
		for _, rf := range group {
			if rf.depth == min {
				top = append(top, rf)
				if rf.tag != "" {
					tagged = append(tagged, rf)
				}
			}
		}
		var win *rawField
		switch {
		case len(top) == 1:
			win = &top[0]
		case len(tagged) == 1:
			win = &tagged[0]
		}
		if win != nil {
			b.addField(s, n, win.path, win.v)
			s.Fields[len(s.Fields)-1].Promoted = win.depth > 0
			if win.quoted {
				f := s.Fields[len(s.Fields)-1]
				if f.Shape.Kind == KScalar {
					q := *f.Shape
					q.Quoted = true
					f.Shape = &q
				}
			}
		}
	}
}

func validJSONTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c > 127):
			return false
		}
	}
	return true
}

// mapstructureFields follows decodeStructFromMap in
// github.com/go-viper/mapstructure/v2, which viper and koanf use.
func (b *schemaBuilder) mapstructureFields(s *Shape, st *types.Struct, prefix string) {
	tagName := b.dec.tag()
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i)).Get(tagName)
		parts := strings.Split(tag, ",")
		squash, remain := false, false
		for _, o := range parts[1:] {
			if o == "squash" {
				squash = true
				break
			}
			if o == "remain" {
				remain = true
				break
			}
		}
		goName := prefix + f.Name()
		if squash {
			if _, ok := f.Type().Underlying().(*types.Interface); ok {
				s.CatchAll = &Shape{Kind: KAny}
				continue
			}
			_, est := derefStruct(f.Type())
			if est == nil {
				b.err = fmt.Errorf("field %s of %s: mapstructure can't squash %s", f.Name(), s.TypeName, typeString(f.Type()))
				return
			}
			b.mapstructureFields(s, est, goName+".")
			continue
		}
		if !f.Exported() {
			continue
		}
		if remain {
			s.CatchAll = &Shape{Kind: KAny}
			if m, ok := f.Type().Underlying().(*types.Map); ok {
				s.CatchAll = b.shape(m.Elem())
			}
			continue
		}
		key := parts[0]
		if key == "" {
			key = f.Name()
		}
		if key == "-" {
			continue
		}
		b.addField(s, key, goName, f)
	}
}

// lookup finds the field a config key fills. folded is true when only a
// case-insensitive match was found.
func (s *Shape) lookup(dec Decoder, key string) (f *Field, folded bool) {
	for _, f := range s.Fields {
		if f.Key == key {
			return f, false
		}
	}
	for _, f := range s.Fields {
		if strings.EqualFold(f.Key, key) {
			return f, true
		}
	}
	return nil, false
}

// detectDecoder picks the decoder from the struct tags the type tree uses.
func detectDecoder(tn *types.TypeName) (Decoder, string, error) {
	counts := map[Decoder]int{}
	seen := map[types.Type]bool{}
	var walk func(t types.Type, depth int)
	walk = func(t types.Type, depth int) {
		if depth > 12 || seen[t] {
			return
		}
		seen[t] = true
		switch u := t.Underlying().(type) {
		case *types.Pointer:
			walk(u.Elem(), depth+1)
		case *types.Slice:
			walk(u.Elem(), depth+1)
		case *types.Array:
			walk(u.Elem(), depth+1)
		case *types.Map:
			walk(u.Elem(), depth+1)
		case *types.Struct:
			// Only look at types from the same module path prefix as the root,
			// so library types with their own tags don't vote.
			if n, ok := t.(*types.Named); ok && depth > 0 && n.Obj().Pkg() != nil && tn.Pkg() != nil && !sameModuleGuess(n.Obj().Pkg().Path(), tn.Pkg().Path()) {
				return
			}
			for i := 0; i < u.NumFields(); i++ {
				tag := reflect.StructTag(u.Tag(i))
				for _, d := range decoders {
					if _, ok := tag.Lookup(d.tag()); ok {
						counts[d]++
					}
				}
				walk(u.Field(i).Type(), depth+1)
			}
		}
	}
	walk(tn.Type(), 0)
	var found []Decoder
	for _, d := range decoders {
		if counts[d] > 0 {
			found = append(found, d)
		}
	}
	total := 0
	for _, d := range found {
		total += counts[d]
	}
	var names []string
	for _, d := range found {
		names = append(names, fmt.Sprintf("%d %s", counts[d], d.tag()))
	}
	switch len(found) {
	case 1:
		return found[0], fmt.Sprintf("from its %s tags", found[0].tag()), nil
	case 0:
		return "", "", fmt.Errorf("%s has no yaml, json, mapstructure or koanf tags, so the decoder can't be guessed; pass --decoder yaml, json, viper or koanf", tn.Name())
	}
	for _, d := range found {
		if counts[d]*10 >= total*9 {
			return d, fmt.Sprintf("from its tags: %s; pass --decoder to change", strings.Join(names, ", ")), nil
		}
	}
	return "", "", fmt.Errorf("%s has tags for more than one decoder (%s); pass --decoder yaml, json, viper or koanf to say which one reads the file", tn.Name(), strings.Join(names, ", "))
}

// sameModuleGuess treats two packages as one project when they share the
// first three path elements (host/owner/repo).
func sameModuleGuess(a, b string) bool {
	pa, pb := strings.SplitN(a, "/", 4), strings.SplitN(b, "/", 4)
	n := 3
	if len(pa) < n || len(pb) < n {
		return pa[0] == pb[0]
	}
	for i := 0; i < n; i++ {
		if pa[i] != pb[i] {
			return false
		}
	}
	return true
}
