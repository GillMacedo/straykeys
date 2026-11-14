// Command straykeys checks YAML and JSON config files against the Go struct
// they are decoded into, and reports keys the program would silently ignore.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const version = "0.1.0"

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

type options struct {
	dir, typ, decoder, cmKey, at, failOn, format string
	allow                                        listFlag
	noViperGets                                  bool
	files                                        []string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: straykeys --type PKG.Type [flags] CONFIG...

Checks YAML and JSON config files against the Go struct they are decoded
into, and reports keys the program would ignore. CONFIG may be - for stdin.

flags:
`

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("straykeys", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.StringVar(&o.dir, "dir", ".", "directory inside the Go module that declares the type")
	fs.StringVar(&o.typ, "type", "", "the struct the config is decoded into, as package.Type: ./internal/config.Config, example.com/app/config.Config, or Config for the package in --dir")
	fs.StringVar(&o.decoder, "decoder", "", "yaml (gopkg.in/yaml.v3), json (encoding/json, sigs.k8s.io/yaml), viper or koanf; default: guessed from the struct tags")
	fs.StringVar(&o.cmKey, "configmap-key", "", "read the config from this data key of each Kubernetes ConfigMap in the files")
	fs.StringVar(&o.at, "at", "", "check only the section at this dotted key path, for configs nested under a top-level key")
	fs.Var(&o.allow, "allow", "a dotted key path to accept without checking, such as one read elsewhere (repeatable)")
	fs.BoolVar(&o.noViperGets, "no-viper-gets", false, "with --decoder viper, don't count keys read with viper.Get* calls as used")
	fs.StringVar(&o.failOn, "fail-on", "error", "exit 1 when there are findings: error, or never")
	fs.StringVar(&o.format, "format", "text", "text or json")
	showVersion := fs.Bool("version", false, "print the version")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(stdout)
			fmt.Fprint(stdout, usage)
			fs.PrintDefaults()
			return 0
		}
		fmt.Fprintf(stderr, "straykeys: %v (run straykeys --help for the flags)\n", err)
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "straykeys", version)
		return 0
	}
	o.files = fs.Args()
	if err := o.validate(); err != nil {
		fmt.Fprintf(stderr, "straykeys: %v\n", err)
		return 2
	}
	rep, err := check(o)
	if err != nil {
		var u untrusted
		if errors.As(err, &u) {
			fmt.Fprintf(stderr, "straykeys: can't trust the input, nothing was reported: %v\n", err)
		} else {
			fmt.Fprintf(stderr, "straykeys: %v\n", err)
		}
		return 2
	}
	if o.format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
	} else {
		rep.writeText(stdout)
	}
	if len(rep.Findings) > 0 && o.failOn == "error" {
		return 1
	}
	return 0
}

func (o *options) validate() error {
	if o.typ == "" {
		return errors.New("--type is required, for example --type ./internal/config.Config")
	}
	if len(o.files) == 0 {
		return errors.New("no config files given")
	}
	switch o.failOn {
	case "error", "never":
	default:
		return fmt.Errorf("--fail-on %q: use error or never", o.failOn)
	}
	switch o.format {
	case "text", "json":
	default:
		return fmt.Errorf("--format %q: use text or json", o.format)
	}
	if o.decoder != "" {
		ok := false
		for _, d := range decoders {
			if string(d) == o.decoder {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("--decoder %q: use yaml, json, viper or koanf", o.decoder)
		}
	}
	stdin := 0
	for _, f := range o.files {
		if f == "-" {
			stdin++
		}
	}
	if stdin > 1 {
		return errors.New("- (stdin) can be given only once")
	}
	return nil
}

// Report is the result of a check.
type Report struct {
	Version       string    `json:"version"`
	Type          string    `json:"type"`
	Decoder       string    `json:"decoder"`
	DecoderReason string    `json:"decoderReason"`
	Configs       []string  `json:"configs"`
	Keys          int       `json:"keysChecked"`
	Opaque        int       `json:"valuesNotChecked"`
	ViperGets     int       `json:"keysReadWithViperGet,omitempty"`
	PlainCopies   int       `json:"typesDecodedThroughPlainCopy,omitempty"`
	Findings      []Finding `json:"findings"`
}

func check(o options) (*Report, error) {
	if err := statDir(o.dir); err != nil {
		return nil, refuse("--dir: %v", err)
	}
	pkgPattern, typeName, err := splitTypeFlag(o.typ)
	if err != nil {
		return nil, err
	}
	// Read the configs first: they are cheap to refuse.
	var docs []*configDoc
	for _, f := range o.files {
		ds, err := loadConfigs(f, o.cmKey, o.at)
		if err != nil {
			return nil, err
		}
		docs = append(docs, ds...)
	}
	tn, l, err := loadType(o.dir, pkgPattern, typeName)
	if err != nil {
		return nil, untrusted{err.Error()}
	}
	dec := Decoder(o.decoder)
	reason := "from --decoder"
	if dec == "" {
		dec, reason, err = detectDecoder(tn)
		if err != nil {
			return nil, untrusted{err.Error()}
		}
	}
	root, plain, err := buildSchema(dec, l, tn)
	if err != nil {
		return nil, untrusted{err.Error()}
	}
	rep := &Report{
		Version: version, Type: tn.Pkg().Path() + "." + tn.Name(), Decoder: string(dec), DecoderReason: reason,
		Findings: []Finding{}, PlainCopies: plain,
	}
	if dec == DecViper && !o.noViperGets {
		modRoot := o.dir
		if lp := l.byPath[tn.Pkg().Path()]; lp != nil && lp.Module != nil && lp.Module.Dir != "" {
			modRoot = lp.Module.Dir
		}
		keys, err := viperReadKeys(modRoot)
		if err != nil {
			return nil, untrusted{fmt.Sprintf("scanning for viper.Get calls: %v", err)}
		}
		rep.ViperGets = addReadKeys(root, keys)
	}
	allowed := map[string]bool{}
	for _, a := range o.allow {
		allowed[strings.ToLower(a)] = true
	}
	for _, d := range docs {
		rootNode := resolve(d.Root)
		if rootNode.Kind != 4 /* yaml.MappingNode */ {
			return nil, refuse("%s: the top level is %s, not a mapping of keys, so it is not a config for %s", d.Name, nodeKind(rootNode), tn.Name())
		}
		if len(rootNode.Content) == 0 {
			return nil, refuse("%s has no keys, so there is nothing to check", d.Name)
		}
		c := &checker{dec: dec, root: root, doc: d, allowed: allowed}
		c.checkDoc()
		if c.topKnown == 0 {
			var ks []string
			for i := 0; i+1 < len(rootNode.Content) && len(ks) < 6; i += 2 {
				ks = append(ks, short(rootNode.Content[i].Value))
			}
			return nil, refuse("%s: none of its top-level keys (%s) is a field of %s, so it looks like another file, the wrong --type, or a config nested under a key (use --at); %s accepts: %s",
				d.Name, strings.Join(ks, ", "), tn.Name(), tn.Name(), keyList(root))
		}
		rep.Configs = append(rep.Configs, d.Name)
		rep.Keys += c.keys
		rep.Opaque += c.opaque
		rep.Findings = append(rep.Findings, c.findings...)
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		a, b := rep.Findings[i], rep.Findings[j]
		if a.Config != b.Config {
			return false
		}
		return a.Line < b.Line
	})
	return rep, nil
}

func (r *Report) writeText(w io.Writer) {
	files := "1 config"
	if len(r.Configs) != 1 {
		files = fmt.Sprintf("%d configs", len(r.Configs))
	}
	fmt.Fprintf(w, "straykeys %s: %s, decoder %s (%s); %s, %d keys\n", r.Version, r.Type, r.Decoder, r.DecoderReason, files, r.Keys)
	for _, f := range r.Findings {
		loc := f.Config
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			if f.Config != f.File {
				loc += " " + strings.TrimPrefix(f.Config, f.File+" ")
			}
		}
		head := strings.TrimRight(fmt.Sprintf("%s %s  %s  %s", f.Level, f.Code, loc, f.Key), " ")
		fmt.Fprintf(w, "\n%s\n  %s\n", head, f.Message)
		if f.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", f.Fix)
		}
	}
	fmt.Fprintln(w)
	where := files
	if len(r.Configs) == 1 {
		where = r.Configs[0]
	}
	var notes []string
	if r.Opaque > 0 {
		notes = append(notes, fmt.Sprintf("%d values decode themselves (UnmarshalYAML, UnmarshalJSON or UnmarshalText), so what is inside them was not checked.", r.Opaque))
	}
	if r.ViperGets > 0 {
		notes = append(notes, fmt.Sprintf("%d keys count as used because the code reads them with viper.Get calls.", r.ViperGets))
	}
	note := ""
	if len(notes) > 0 {
		note = " " + strings.Join(notes, " ")
	}
	if len(r.Findings) == 0 {
		fmt.Fprintf(w, "No problems: all %d keys in %s match fields of %s.%s\n", r.Keys, where, shortType(r.Type), note)
		return
	}
	configs := map[string]bool{}
	for _, f := range r.Findings {
		configs[f.Config] = true
	}
	plural := "s"
	if len(r.Findings) == 1 {
		plural = ""
	}
	if len(r.Configs) == 1 {
		fmt.Fprintf(w, "%d error%s in %s.%s\n", len(r.Findings), plural, where, note)
		return
	}
	fmt.Fprintf(w, "%d error%s in %d of %s.%s\n", len(r.Findings), plural, len(configs), files, note)
}

func shortType(t string) string {
	return filepath.Base(t)
}
