# straykeys

Claude Code skill and Go CLI that checks YAML and JSON config files against the Go struct they decode into, and reports keys the program ignores.

## Architecture
`cmd/straykeys/main.go` holds the CLI and the text report. `cmd/straykeys/load.go` runs `go list -deps -json` and type-checks the package from source (`loadType`). `cmd/straykeys/schema.go` turns the struct into the keys each decoder accepts (`buildSchema`, `yamlFields`, `jsonFields`, `mapstructureFields`, `decodesPlainCopy`). `cmd/straykeys/config.go` reads configs and ConfigMaps (`loadConfigs`). `cmd/straykeys/check.go` walks a config against the schema and makes the findings (`checker.walk`, `walkKey`, `scalarProblem`). `cmd/straykeys/viperkeys.go` finds `viper.Get*` keys. `skills/checking-config-keys-against-go-structs/SKILL.md` tells Claude how to use it. See `docs/architecture.md`.

## Commands
- `make test`: gofmt, vet, the tests in `cmd/straykeys` and `tests/`, and `tests/decoders`, which compares straykeys with the real decoders
- `make test-decoders`: only the decoder comparison; `STRAYKEYS_MUTANTS=400 STRAYKEYS_SEED=3` runs more configs
- `make lint`: plugin manifest validation (needs Claude Code)
- `make demo`: run on the example service

## Conventions
- The root module has one dependency, gopkg.in/yaml.v3, vendored, because the installed plugin is the whole repository and must build with no network. Don't add others.
- Each decoder rule must match the library's source. Prove it in `tests/decoders` before changing `schema.go` or `check.go`.
- Any input it can't fully read or trust exits 2 with nothing on stdout, whatever `--fail-on` says. Add a case to `badConfigs`, `badJSON` or `TestRefusesBadSources` in `tests/` for each new kind of input.
- Never run the user's program or download modules: `go list` runs with `GOPROXY=off`.
- Tests must not assume the machine they run on: use `runtime` or `go env` for anything platform-specific.

## Do and don't
- Do run `make test` on Go 1.22 and on the current Go release before calling a change done. `GOTOOLCHAIN=go1.22.12 make test` works.
- Don't print "No problems" for a run that checked nothing.
- Don't change the example configs without updating the README output; `tests/readme_test.go` compares them.

## Definition of done
`make test` and `make lint` pass, README output matches a real run, and `ROADMAP.md` and docs are updated when relevant.
