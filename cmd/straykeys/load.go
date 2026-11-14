package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// listedPackage is the part of `go list -json` output this tool needs.
type listedPackage struct {
	ImportPath string
	Dir        string
	Name       string
	GoFiles    []string
	CgoFiles   []string
	ImportMap  map[string]string
	Standard   bool
	DepOnly    bool
	Module     *struct {
		Path string
		Dir  string
	}
	Error *struct {
		Err string
	}
}

// loader type-checks a package and its dependencies from source. Function
// bodies are skipped: only declarations matter for a struct's fields.
type loader struct {
	fset    *token.FileSet
	byPath  map[string]*listedPackage
	byDir   map[string]*listedPackage
	checked map[string]*types.Package
	errs    map[string][]error
	files   map[string][]*ast.File
}

// splitTypeFlag turns "./internal/config.Config" into the package pattern and
// the type name.
func splitTypeFlag(s string) (pkg, name string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", errors.New("--type is required, for example --type ./internal/config.Config")
	}
	i := strings.LastIndex(s, ".")
	if i < 0 {
		return ".", s, nil
	}
	pkg, name = s[:i], s[i+1:]
	if name == "" || strings.ContainsAny(name, "/\\") || !token.IsIdentifier(name) {
		return "", "", fmt.Errorf("--type %q: expected a package and a type name, such as ./internal/config.Config", s)
	}
	if pkg == "" {
		pkg = "."
	}
	return pkg, name, nil
}

// loadType finds the named type in the package that pattern names, run from dir.
func loadType(dir, pattern, name string) (*types.TypeName, *loader, error) {
	cmd := exec.Command("go", "list", "-deps", "-json", "--", pattern)
	cmd.Dir = dir
	// Never download: the module's dependencies must already be in the module
	// cache or in vendor/.
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		hint := ""
		if strings.Contains(msg, "GOPROXY=off") || strings.Contains(msg, "module lookup disabled") {
			hint = "\n  straykeys doesn't download modules; run go mod download in the module first"
		}
		return nil, nil, fmt.Errorf("go list %s failed in %s: %s%s", pattern, dir, firstLines(msg, 6), hint)
	}
	l := &loader{
		fset:    token.NewFileSet(),
		byPath:  map[string]*listedPackage{},
		byDir:   map[string]*listedPackage{},
		checked: map[string]*types.Package{},
		errs:    map[string][]error{},
		files:   map[string][]*ast.File{},
	}
	dec := json.NewDecoder(&stdout)
	var targets []*listedPackage
	for {
		p := new(listedPackage)
		if err := dec.Decode(p); err == io.EOF {
			break
		} else if err != nil {
			return nil, nil, fmt.Errorf("reading go list output: %v", err)
		}
		l.byPath[p.ImportPath] = p
		l.byDir[p.Dir] = p
		if !p.DepOnly {
			targets = append(targets, p)
		}
	}
	if len(targets) != 1 {
		return nil, nil, fmt.Errorf("--type package %q matched %d packages; name exactly one", pattern, len(targets))
	}
	target := targets[0]
	if target.Error != nil {
		return nil, nil, fmt.Errorf("package %s: %s", target.ImportPath, target.Error.Err)
	}
	if len(target.GoFiles)+len(target.CgoFiles) == 0 {
		return nil, nil, fmt.Errorf("package %s has no Go files for this platform", target.ImportPath)
	}
	pkg, err := l.check(target.ImportPath)
	if err != nil {
		return nil, nil, err
	}
	if errs := l.errs[target.ImportPath]; len(errs) > 0 {
		var lines []string
		for i, e := range errs {
			if i == 5 {
				lines = append(lines, fmt.Sprintf("and %d more", len(errs)-5))
				break
			}
			lines = append(lines, e.Error())
		}
		return nil, nil, fmt.Errorf("package %s does not type-check, so its fields can't be trusted:\n  %s", target.ImportPath, strings.Join(lines, "\n  "))
	}
	obj := pkg.Scope().Lookup(name)
	if obj == nil {
		var names []string
		for _, n := range pkg.Scope().Names() {
			if tn, ok := pkg.Scope().Lookup(n).(*types.TypeName); ok {
				if _, ok := tn.Type().Underlying().(*types.Struct); ok {
					names = append(names, n)
				}
			}
		}
		sort.Strings(names)
		hint := "it declares no struct types"
		if len(names) > 0 {
			hint = "struct types there: " + strings.Join(names, ", ")
		}
		return nil, nil, fmt.Errorf("type %s not found in package %s; %s", name, target.ImportPath, hint)
	}
	tn, ok := obj.(*types.TypeName)
	if !ok {
		return nil, nil, fmt.Errorf("%s.%s is not a type", target.ImportPath, name)
	}
	t := tn.Type()
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	if _, ok := t.Underlying().(*types.Struct); !ok {
		return nil, nil, fmt.Errorf("%s.%s is %s, not a struct", target.ImportPath, name, t.Underlying())
	}
	if tn.Type().(*types.Named).TypeParams().Len() > 0 {
		return nil, nil, fmt.Errorf("%s.%s is generic; name a non-generic struct type", target.ImportPath, name)
	}
	return tn, l, nil
}

func (l *loader) check(path string) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if p, ok := l.checked[path]; ok {
		if p == nil {
			return nil, fmt.Errorf("import cycle through %s", path)
		}
		return p, nil
	}
	lp, ok := l.byPath[path]
	if !ok {
		return nil, fmt.Errorf("package %s is not in go list output", path)
	}
	if lp.Error != nil {
		return nil, fmt.Errorf("package %s: %s", path, lp.Error.Err)
	}
	l.checked[path] = nil
	var files []*ast.File
	names := append(append([]string{}, lp.GoFiles...), lp.CgoFiles...)
	for _, n := range names {
		f, err := parser.ParseFile(l.fset, filepath.Join(lp.Dir, n), nil, parser.SkipObjectResolution|parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %v", filepath.Join(lp.Dir, n), err)
		}
		files = append(files, f)
	}
	l.files[path] = files
	conf := types.Config{
		IgnoreFuncBodies: true,
		FakeImportC:      len(lp.CgoFiles) > 0,
		Importer:         importerFor{l, lp},
		Error: func(err error) {
			l.errs[path] = append(l.errs[path], err)
		},
	}
	pkg, _ := conf.Check(path, l.fset, files, nil)
	if pkg == nil {
		return nil, fmt.Errorf("type-checking %s failed", path)
	}
	l.checked[path] = pkg
	return pkg, nil
}

type importerFor struct {
	l  *loader
	lp *listedPackage
}

func (i importerFor) Import(path string) (*types.Package, error) {
	if mapped, ok := i.lp.ImportMap[path]; ok {
		path = mapped
	}
	return i.l.check(path)
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("(%d more lines)", len(lines)-n))
	}
	return strings.Join(lines, "\n  ")
}

// statDir checks that dir exists and is a directory.
func statDir(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}
