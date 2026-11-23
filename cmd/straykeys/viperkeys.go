package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// viperReaders are viper functions whose first argument is a config key.
var viperReaders = map[string]bool{
	"Get": true, "GetString": true, "GetBool": true, "GetInt": true, "GetInt32": true, "GetInt64": true,
	"GetUint": true, "GetUint8": true, "GetUint16": true, "GetUint32": true, "GetUint64": true,
	"GetFloat64": true, "GetTime": true, "GetDuration": true, "GetIntSlice": true, "GetStringSlice": true,
	"GetStringMap": true, "GetStringMapString": true, "GetStringMapStringSlice": true, "GetSizeInBytes": true,
	"IsSet": true, "Sub": true, "UnmarshalKey": true, "InConfig": true,
}

// viperInstanceNames returns identifiers in f that hold a *viper.Viper value:
// those assigned from viper.New() and those declared with that type. The
// viper import is bound to `local` in this file.
func viperInstanceNames(f *ast.File, local string) map[string]bool {
	names := map[string]bool{}
	isViperType := func(expr ast.Expr) bool {
		star, ok := expr.(*ast.StarExpr)
		if !ok {
			return false
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "Viper" && isIdent(sel.X, local)
	}
	isNewCall := func(expr ast.Expr) bool {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "New" && isIdent(sel.X, local)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			if len(stmt.Rhs) == 0 {
				return true
			}
			for _, rhs := range stmt.Rhs {
				if isNewCall(rhs) {
					for _, lhs := range stmt.Lhs {
						if id, ok := lhs.(*ast.Ident); ok {
							names[id.Name] = true
						}
					}
				}
			}
		case *ast.GenDecl:
			if stmt.Tok != token.VAR {
				return true
			}
			for _, spec := range stmt.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || vs.Type == nil || !isViperType(vs.Type) {
					continue
				}
				for _, name := range vs.Names {
					names[name.Name] = true
				}
			}
		}
		return true
	})
	return names
}

func isIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

// viperReadKeys finds keys the module reads with package-level viper calls
// such as viper.GetString("server.port"), with the file and line of the first
// read of each key.
func viperReadKeys(root string) (map[string]string, error) {
	keys := map[string]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return nil // the type check reports broken files that matter
		}
		local := ""
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "github.com/spf13/viper" {
				local = "viper"
				if imp.Name != nil {
					local = imp.Name.Name
				}
			}
		}
		if local == "" || local == "_" {
			return nil
		}
		instances := viperInstanceNames(f, local)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !viperReaders[sel.Sel.Name] {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || (id.Name != local && !instances[id.Name]) {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			k, err := strconv.Unquote(lit.Value)
			if err != nil || k == "" {
				return true
			}
			k = strings.ToLower(k)
			if _, seen := keys[k]; !seen {
				pos := fset.Position(lit.Pos())
				rel, err := filepath.Rel(root, pos.Filename)
				if err != nil {
					rel = pos.Filename
				}
				keys[k] = rel + ":" + strconv.Itoa(pos.Line)
			}
			return true
		})
		return nil
	})
	return keys, err
}

// addReadKeys adds keys read with viper.Get* to the schema, so the file's
// values for them count as used.
func addReadKeys(root *Shape, keys map[string]string) int {
	var names []string
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	added := 0
	for _, k := range names {
		s := root
		segs := strings.Split(k, ".")
		for i, seg := range segs {
			if s.Kind != KStruct {
				break
			}
			f, _ := s.lookup(DecViper, seg)
			if f == nil {
				shape := &Shape{Kind: KAny, TypeName: "a value read with viper"}
				if i < len(segs)-1 {
					shape = &Shape{Kind: KStruct, TypeName: "keys read with viper"}
				}
				f = &Field{Key: seg, GoName: "viper." + "Get(\"" + strings.Join(segs[:i+1], ".") + "\") at " + keys[k], Shape: shape}
				s.Fields = append(s.Fields, f)
				added++
			}
			s = f.Shape
		}
	}
	return added
}
