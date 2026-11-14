# Security

Report vulnerabilities through [GitHub's private vulnerability reporting](https://github.com/GillMacedo/straykeys/security/advisories/new), not in a public issue.

`straykeys` reads the config files you pass it and runs `go list` in `--dir` with `GOPROXY=off`, then parses and type-checks Go source. It doesn't build or run your code, use the network, or write files. Findings quote config keys and short values, and config files often hold secrets, so check its output before sharing it.

A missed or wrong finding is a normal bug. If `straykeys` writes a file, opens a network connection, runs code from the module, or reports a clean result for input it couldn't read, report it privately. `straykeys` is a diagnostic aid and doesn't enforce anything.
