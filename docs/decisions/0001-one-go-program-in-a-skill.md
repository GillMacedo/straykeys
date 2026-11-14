# 0001: One Go program in a skill

## Context
The tool reads files and prints a report. An agent can run a program from a skill; nothing needs a long-running server. Reading Go types exactly needs `go/types`, so the program is in Go, and users of the tool already have the Go toolchain.

## Decision
Ship a Claude Code skill with one Go program in `cmd/straykeys`, built by the skill with `go build`. No MCP server.

## Consequences
Users can also run it in CI with `go run github.com/GillMacedo/straykeys/cmd/straykeys@v0.1.0`, and `go install` names the binary `straykeys`.
