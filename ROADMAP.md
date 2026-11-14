# Roadmap

## 0.1.0 (this release)
- Check YAML and JSON configs against the Go struct they decode into, for yaml.v3, encoding/json, sigs.k8s.io/yaml, viper and koanf, and report ignored keys, durations without units and values the decoder refuses.

## Next
- TOML, for BurntSushi/toml and pelletier/go-toml v2, which viper and koanf also read.
- gopkg.in/yaml.v2 and goccy/go-yaml as decoders, with their own suites in `tests/decoders`.
- `--decode-hook` for viper and koanf programs that add hooks such as `TextUnmarshallerHookFunc`, so those types are checked by their fields.
- Find `viper.Get*` calls on `*viper.Viper` values, not only the package functions.
- A `--from-call` mode that finds the struct from the `Unmarshal` call, so `--type` and `--decoder` are optional.
- Report fields no config sets, for users who ask, to find settings nobody configures.

## Not planned
- Running the program or connecting to a cluster. straykeys reads files.
- Checking values against business rules. That belongs in the program's own validation.
