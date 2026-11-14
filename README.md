# straykeys: find the config keys your Go program silently ignores

[![CI](https://github.com/GillMacedo/straykeys/actions/workflows/ci.yml/badge.svg)](https://github.com/GillMacedo/straykeys/actions/workflows/ci.yml) [![MIT license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**A Claude Code skill and Go CLI for Go developers and the people who deploy Go services. It checks YAML and JSON config files against the Go struct they are decoded into, and lists every key the program would ignore: typos, keys in the wrong case, keys at the wrong level, fields renamed in code and left unchanged in deployed configs, numbers for `time.Duration` fields, and values the decoder would refuse. It knows the key rules of gopkg.in/yaml.v3, encoding/json, sigs.k8s.io/yaml, viper and koanf, and reads configs inside Kubernetes ConfigMaps.**

By default, `yaml.Unmarshal`, `json.Unmarshal`, `viper.Unmarshal` and `koanf.Unmarshal` drop any key that matches no field, without an error. A config with `backof: 1s` or `maxOpenConns: 50` loads, the service starts, and it runs with the defaults. Strict modes exist (`KnownFields`, `DisallowUnknownFields`, `UnmarshalExact`), but they fail at startup in the environment that has the bad file, and many programs don't turn them on. straykeys finds these keys in a pull request or a CI step, before the config ships, from the struct itself.

It reads files only. It loads your Go package with `go list` and `go/types` (no build, no run), and it doesn't use the network: the module's dependencies must already be downloaded or vendored.

## What you can ask Claude

- "We set `retry.backoff` to 1s in prod, but the logs show the default. Is the key right?"
- "Check every config in `deploy/` against our `config.Config` struct."
- "I renamed `Timeout` to `RequestTimeout` in the config struct. Which config files still use the old key?"
- "Does this ConfigMap's `config.yaml` match what the service reads?"
- "Add a CI step that fails when a config file has a key our Go code doesn't know."

## Install

As a Claude Code plugin:

    claude plugin marketplace add GillMacedo/straykeys
    claude plugin install straykeys@straykeys-marketplace

Or run it with Go 1.22 or newer:

    go run github.com/GillMacedo/straykeys/cmd/straykeys@v0.1.0 --type ./internal/config.Config config.yaml

## Example

`examples/billing` is a made-up service that reads its config with gopkg.in/yaml.v3. `config.prod.yaml` loads without an error, and four of its settings never reach the program. `--fail-on never` keeps the exit code at 0 for this demo; without it, the run exits 1:

    $ go run ./cmd/straykeys --fail-on never --dir examples/billing --type Config examples/billing/config.prod.yaml
    straykeys 0.1.0: github.com/GillMacedo/straykeys/examples/billing.Config, decoder yaml (from its yaml tags); 1 config, 18 keys

    ERROR unknown-key  examples/billing/config.prod.yaml:4  database.maxOpenConns
      database.maxOpenConns is not a field of billing.Database, so the program ignores it
      fix: did you mean database.max_open_conns? (field MaxOpenConns)

    ERROR unknown-key  examples/billing/config.prod.yaml:8  retry.backof
      retry.backof is not a field of billing.Retry, so the program ignores it
      fix: did you mean retry.backoff? (field Backoff)

    ERROR unknown-key  examples/billing/config.prod.yaml:14  invoices.notify[0].onFailed
      invoices.notify[0].onFailed is not a field of billing.Notify, so the program ignores it
      fix: did you mean invoices.notify[0].on_failed? (field OnFailed)

    ERROR unknown-key  examples/billing/config.prod.yaml:18  max_open_conns
      max_open_conns is not a field of billing.Config, so the program ignores it
      fix: max_open_conns is a key of database; move it there

    4 errors in examples/billing/config.prod.yaml.

The service would run with an unlimited connection pool, no retry backoff, and no failure emails. A test in `tests/billing_test.go` loads this file with the example's own `Load` function to confirm that, and yaml.v3's strict mode names the same four lines.

`config.dev.yaml` is correct, and straykeys stays quiet:

    $ go run ./cmd/straykeys --dir examples/billing --type Config examples/billing/config.dev.yaml
    straykeys 0.1.0: github.com/GillMacedo/straykeys/examples/billing.Config, decoder yaml (from its yaml tags); 1 config, 17 keys

    No problems: all 17 keys in examples/billing/config.dev.yaml match fields of billing.Config.

The same check works on a config inside a Kubernetes ConfigMap. Line numbers point into the manifest:

    $ go run ./cmd/straykeys --fail-on never --dir examples/billing --type Config --configmap-key config.yaml examples/billing/deploy/configmap.yaml
    straykeys 0.1.0: github.com/GillMacedo/straykeys/examples/billing.Config, decoder yaml (from its yaml tags); 1 config, 12 keys

    ERROR duration-without-unit  examples/billing/deploy/configmap.yaml:11 (ConfigMap billing-config, key config.yaml)  database.conn_timeout
      database.conn_timeout is the number 5 with no unit, for a time.Duration field: yaml.v3 refuses to load it (yaml.v2 would read it as 5 nanoseconds)
      fix: write it with a unit, for example "5s" or "5ms"

    ERROR unknown-key  examples/billing/deploy/configmap.yaml:16 (ConfigMap billing-config, key config.yaml)  invoices.currancy
      invoices.currancy is not a field of billing.Invoices, so the program ignores it
      fix: did you mean invoices.currency? (field Currency)

    2 errors in examples/billing/deploy/configmap.yaml (ConfigMap billing-config, key config.yaml).

Add `--format json` for machine-readable output.

## In CI

Run it from the module that declares the struct, after the module's dependencies are downloaded:

    - run: go mod download
    - run: go run github.com/GillMacedo/straykeys/cmd/straykeys@v0.1.0 --type ./internal/config.Config deploy/*.yaml
    - run: helm template ./chart | go run github.com/GillMacedo/straykeys/cmd/straykeys@v0.1.0 --type ./internal/config.Config --configmap-key config.yaml -

## What it reads

- **The struct (`--type`, with `--dir`, default `.`):** `package.Type`, such as `./internal/config.Config`, `example.com/app/config.Config`, or `Config` for the package in `--dir`. straykeys runs `go list -deps -json` in `--dir` and type-checks the package and its dependencies from source, skipping function bodies. Fields come from the decoder's own rules, below. A type with an `UnmarshalYAML` or `UnmarshalJSON` method that only sets defaults and decodes a plain copy (`type plain Config`, common in Prometheus projects) is checked by its fields. Types that decode themselves in other ways, or with `UnmarshalText`, are skipped, and the summary counts them.
- **The decoder (`--decoder`):** guessed from the struct tags when one kind makes up at least 90% of them; otherwise pass it.
- **Config files (one or more, or `-` for stdin):** YAML, or JSON when the name ends in `.json`. With `--configmap-key KEY`, each file is a Kubernetes manifest (several documents or a `List`), and the config is the value of `data.KEY` in each ConfigMap. `--at a.b` checks only the section at that key path.

| `--decoder` | Libraries | Tag | Key matching |
|---|---|---|---|
| `yaml` | gopkg.in/yaml.v3, go.yaml.in/yaml/v3 | `yaml` | exact; a field without a tag uses its name in lower case; `,inline` structs and maps |
| `json` | encoding/json, sigs.k8s.io/yaml, github.com/ghodss/yaml | `json` | exact, then case-insensitive; embedded structs promoted by encoding/json's rules, including the ones that drop a name two embedded structs share; `,string` |
| `viper` | github.com/spf13/viper `Unmarshal` | `mapstructure` | case-insensitive; `,squash`, `,remain`; keys with dots are nested; keys read with `viper.Get*("...")` count as used |
| `koanf` | github.com/knadh/koanf `Unmarshal` | `koanf` | case-insensitive; `,squash`, `,remain` |

## What it reports

| Code | Meaning |
|---|---|
| `unknown-key` | No field matches the key, so the program ignores it. The fix names the closest field, or the place in the struct where the key belongs |
| `wrong-case` | yaml.v3 matches keys exactly, and only the case differs from a field's key |
| `duration-without-unit` | A number for a `time.Duration`. yaml.v3 refuses it; encoding/json, viper and koanf read it as nanoseconds. Zero, and numbers of a million or more (likely meant as nanoseconds), are not reported for those three |
| `wrong-type` | A value the decoder refuses for the field's type, such as `"abc"` for an `int`. The program fails at startup |
| `wrong-shape` | A mapping where the field needs a value or list, or the other way round. The program fails at startup |
| `duplicate-key` | A key twice in one mapping (YAML decoders refuse it; encoding/json keeps the last one silently), or two spellings that fill one field |
| `extra-document` | A second YAML document after `---`, which decoders never read |
| `alias-cycle` | A YAML alias inside the node it refers to, which decoders refuse |
| `no-final-newline` | A YAML file without a final line break, which may have been cut off mid-line |

All findings are errors. A key that only holds a YAML anchor (`x-defaults: &defaults`) isn't reported; its values are checked where the alias is used. `--allow a.b` (repeatable) accepts a key path the program reads some other way.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | No findings, or `--fail-on never` |
| 1 | Findings (`--fail-on error`, the default) |
| 2 | The input can't be trusted, nothing was checked, or the flags are wrong. Stdout is empty |

## When it refuses

straykeys exits 2 and prints only the reason when it can't trust what it read, whatever `--fail-on` says. For example, a CI step that saved `kubectl` output with its errors, for a ConfigMap that doesn't exist:

    kubectl get configmap billing-config -o jsonpath='{.data.config\.yaml}' > out.yaml 2>&1
    go run ./cmd/straykeys --dir examples/billing --type Config out.yaml

prints this on stderr. The program exits 2 (under `go run`, Go adds a line `exit status 2` and itself exits 1):

    straykeys: can't trust the input, nothing was reported: out.yaml: none of its top-level keys (Error from server (NotFound)) is a field of Config, so it looks like another file, the wrong --type, or a config nested under a key (use --at); Config accepts: listen, database, retry, invoices, shutdown_timeout, features

It does the same for an empty file, comments only, text, binary data, HTML, invalid YAML or JSON, JSON cut off or followed by more text, a top level that is a list or a single value, `{}`, a file for another program, the output of a failed `go build`, `go vet`, `go test` or `go run`, a missing ConfigMap or data key, and a `--at` path that isn't there. On the Go side: a missing directory, no `go.mod`, a Go file that is empty, cut off or not Go, a package that doesn't type-check, a missing or generic type, a type that isn't a struct, a root type that decodes itself, struct tags yaml.v3 rejects, and tags for several decoders without `--decoder`. A bad file among good ones fails the whole run.

One kind of cut can't be seen: a YAML file cut exactly at the end of a line still parses, and straykeys checks the keys that are there. The header shows how many keys it checked.

## Limits

- **Keys, types and shapes only.** It doesn't check that values are sensible or that required keys are present.
- **Other ways of reading keys.** A program that reads keys outside the struct (`viper.Get` on a `*viper.Viper` value, `os.Getenv`, a second struct) will see them reported. viper's package functions (`viper.GetString("a.b")`) are found in the module's source; for the rest, pass `--allow`.
- **Custom decode hooks.** viper and koanf are modeled with their default decoder settings. A `DecodeHook` that turns strings into structs, or a custom key delimiter, isn't seen.
- **YAML 1.1 values.** yaml.v3 parses YAML 1.2, where `yes` and `on` are strings. sigs.k8s.io/yaml parses YAML 1.1, and straykeys treats plain `yes`, `no`, `on` and `off` as booleans for it. Other YAML 1.1 forms, such as `0777` octal, are read as YAML 1.2.
- **TOML, HCL, INI and env files** aren't read.

## FAQ

**Why does my Go program ignore a key in its YAML config?** gopkg.in/yaml.v3 fills struct fields by their `yaml` tag, or by the field name in lower case, and drops any key that matches nothing. A typo, a different case (`maxConns` for `max_conns`), or a key one level too high or low is dropped without an error.

**How do I make yaml.v3 or encoding/json fail on unknown keys?** Use `yaml.NewDecoder(r)` with `dec.KnownFields(true)`, `json.NewDecoder(r)` with `dec.DisallowUnknownFields()`, or `viper.UnmarshalExact`. Those fail when the program starts. straykeys finds the same keys earlier, in CI, for every config file at once.

**Why is my viper config value always the default?** viper's `Unmarshal` matches keys to `mapstructure` tags without regard to case and ignores the rest. A key under the wrong parent, or a tag that differs from the key, leaves the field at its default.

**Why did my timeout become a few nanoseconds?** encoding/json, viper and koanf read a plain number for a `time.Duration` as nanoseconds, so `timeout: 30` is 30ns. Write `30s`. yaml.v3 refuses the number instead.

**Does it work with Helm charts?** Render the chart with `helm template` and pass the output with `--configmap-key`. It doesn't read templates.

**Is it an MCP server?** No. It's a skill with one Go program, so `go run` is all it needs.

## Tested on

- **The real decoders.** `tests/decoders` is a separate module that decodes configs into test structs with gopkg.in/yaml.v3 v3.0.1, encoding/json, sigs.k8s.io/yaml v1.4.0, viper v1.20.1 and koanf v2.1.2. It makes mutated configs (misspelled, recased, moved and added keys, and values changed to other types), runs straykeys on them, and checks each value: straykeys must report a key exactly when changing its value doesn't change what the library decodes, and must report a load error exactly when the library refuses the file. The CI run covers 11 setups (embedding, `,inline`, `,squash`, `,remain`, anchors and merge keys, dotted keys), 1,320 configs and 12,275 values, 1,200 of which the libraries ignore. By hand, 8 more runs with other seeds and 400 configs per setup (35,200 configs) gave no difference. This found and fixed several mistakes in straykeys's first model of the decoders, such as sigs.k8s.io/yaml not converting values below promoted fields.
- **Real projects, by hand, with Go 1.27.3 on linux/arm64.** [gatus](https://github.com/TwiN/gatus) at 43dd47b: in `.examples/docker-compose-mattermost/config/config.yaml`, `alerting.mattermost.insecure: true` is ignored (the field is `client.insecure`), which yaml.v3's strict mode confirms; seven other example configs were quiet, and two without a final newline got `no-final-newline`. [glance](https://github.com/glanceapp/glance) at 53aec7f: `docs/glance.yml` was quiet. [blackbox_exporter](https://github.com/prometheus/blackbox_exporter) at 655fd77: all 277 keys of `example.yml` matched, as they must, because it loads with `KnownFields(true)`. [dex](https://github.com/dexidp/dex) at a475551: `examples/config-dev.yaml` was quiet, and `config.docker.yaml`, a template, was refused. [golangci-lint](https://github.com/golangci/golangci-lint) at 1bdd8ed: all 1,252 keys of `.golangci.reference.yml` matched with `--decoder viper`, and a planted `errchek` was reported with the right fix.
- **Input it can't trust:** tests pass 19 kinds of bad config and the real output of 5 failed `go build`, `go vet`, `go test` and `go run` commands (alone, next to a good file, and inside a ConfigMap), 9 kinds of bad JSON, 5 bad ConfigMaps, 11 bad flag sets and 25 bad Go sources, under both `--fail-on` values and both formats, and check for exit 2 with nothing on stdout. Others cut the example configs at every byte (refused or reported, and a cut at a line end checks fewer keys), a JSON config at every byte (always refused), and the Go source at every byte (refused, or the same result as the whole file).
- **The installed plugin:** a test copies the repository the way the marketplace installs it and runs the skill's build command with `GOPROXY=off`.

Not tested: macOS and Windows; gopkg.in/yaml.v2, goccy/go-yaml and other YAML libraries; viper and koanf with custom decoder options; programs that read keys in ways other than those listed above; a fresh Claude Code session using the skill. The `go run ...@v0.1.0` lines work only once the repository is published.

## Contributing

Contributions are welcome, from people and from people working with AI agents. See [CONTRIBUTING.md](CONTRIBUTING.md). Good first issues are [listed here](https://github.com/GillMacedo/straykeys/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

### How pull requests work

1. **Open a PR.** Fork the repo, work on a branch, and keep it to one change. Write a title that says what changes for users, because it becomes the changelog line: `feat:` goes under Added and `fix:` under Fixed.
2. **Automatic check.** Code changes need tests, and `make test` must pass. Changes over 30 lines need a linked open issue (`Closes #<number>`), and a PR can have at most 800 changed lines. If something is missing, a comment tells you what to fix.
3. **Review.** Claude reviews each PR automatically. Code PRs also get a security review and are tested in a sandbox. The review then merges the PR or asks for changes, and a small problem may become a follow-up issue instead. A PR with no new commit 14 days after a change request is closed, and you can reopen it. PRs that change CI, agent settings, npm or Python dependencies, vendored code or build files can't be merged this way, and a comment says so. The same goes for anything the security review doesn't clear.
4. **Release.** A merge that changes what users install becomes a new version within about two hours, with a GitHub Release and, if the project has one, its npm package. Docs and CI changes wait for the next release.

## AI disclosure

This project was largely generated with Claude Code and reviewed by its maintainer.

## About me

I'm Giulliano Macedo, and I build developer tools at GoTech Solutions. Follow along or get in touch:

- GitHub: [GillMacedo](https://github.com/GillMacedo)
- LinkedIn: [giulliano](https://www.linkedin.com/in/giulliano/)
- X: [@giumacedo](https://x.com/giumacedo)

If `straykeys` saved you some prompts, a star helps others find it.

---

Built by [Giulliano Macedo](https://github.com/GillMacedo), GoTech Solutions · [LinkedIn](https://www.linkedin.com/in/giulliano/) · [X](https://x.com/giumacedo)
