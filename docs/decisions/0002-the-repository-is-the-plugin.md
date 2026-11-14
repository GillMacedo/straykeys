# 0002: The repository root is the plugin

## Context
Parsing YAML with line numbers, anchors and merge keys needs a YAML library; the standard library has none. Earlier skills in this account keep the plugin in `plugin/` with standard-library code only, because Claude Code installs only that folder, and a folder without `go.mod` can't build code with dependencies.

## Decision
The marketplace lists the plugin with `"source": "./"`. The installed plugin is the whole repository, with `go.mod`, `go.sum` and `vendor/` holding gopkg.in/yaml.v3. The skill builds with `cd "${CLAUDE_PLUGIN_ROOT}" && go build ./cmd/straykeys`.

## Consequences
The plugin builds offline. The install is larger than a single folder, and the root module must not gain more dependencies. `claude plugin validate` warns that `CLAUDE.md` at the plugin root isn't loaded as plugin context; it is there for contributors, so `make lint` runs the validator without `--strict` and fails on any other root warning. `tests/plugin_test.go` copies the repository as an install would and runs the skill's build command with `GOPROXY=off`.
