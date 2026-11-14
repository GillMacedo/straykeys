# Examples

`billing/` is a made-up service. `config.go` declares its config struct and reads the file with gopkg.in/yaml.v3, which ignores unknown keys.

- `config.dev.yaml` matches the struct. straykeys stays quiet.
- `config.prod.yaml` loads without an error and has four keys the service ignores: `maxOpenConns` (the key is `max_open_conns`), `backof`, `onFailed` in a list item, and `max_open_conns` at the top level instead of under `database`.
- `deploy/configmap.yaml` holds a config in a ConfigMap, with a misspelled `currancy` and `conn_timeout: 5`, which yaml.v3 refuses for a `time.Duration`.

Run them from the repository root:

    go run ./cmd/straykeys --dir examples/billing --type Config examples/billing/config.prod.yaml
    go run ./cmd/straykeys --dir examples/billing --type Config examples/billing/config.dev.yaml
    go run ./cmd/straykeys --dir examples/billing --type Config --configmap-key config.yaml examples/billing/deploy/configmap.yaml

`tests/billing_test.go` loads the same files with the example's `Load` function and with yaml.v3's strict mode, to confirm what the README says the service does with them.
