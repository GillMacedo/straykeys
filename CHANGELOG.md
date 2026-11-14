# Changelog

All notable changes are documented here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. This project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-11-14

### Added
- `straykeys` CLI and the `checking-config-keys-against-go-structs` skill: checks YAML and JSON config files against the Go struct they decode into and reports keys the program ignores.
- Decoders: gopkg.in/yaml.v3 (`yaml`), encoding/json and sigs.k8s.io/yaml (`json`), viper (`viper`, including keys read with `viper.Get*` and dotted keys) and koanf (`koanf`), guessed from struct tags or set with `--decoder`.
- Findings: `unknown-key` with a suggested field or location, `wrong-case`, `duration-without-unit`, `wrong-type`, `wrong-shape`, `duplicate-key`, `extra-document`, `alias-cycle` and `no-final-newline`.
- Configs inside Kubernetes ConfigMaps (`--configmap-key`), sections (`--at`), accepted keys (`--allow`), stdin, `--format json` and `--fail-on error|never`.
- `UnmarshalYAML` and `UnmarshalJSON` methods that decode a plain copy of the struct are checked by the struct's fields.
- Refuses with exit 2 on configs or Go packages it can't trust.
