---
name: checking-config-keys-against-go-structs
description: Checks YAML and JSON config files against the Go struct they are decoded into and lists keys the program silently ignores (typos, wrong case, a key at the wrong level, a field renamed in code and left unchanged in deployed configs), numbers given for time.Duration fields, and values the decoder would refuse. Knows the key rules of gopkg.in/yaml.v3, encoding/json, sigs.k8s.io/yaml, viper (mapstructure tags, viper.Get calls, dotted keys) and koanf, and reads configs inside Kubernetes ConfigMaps. Use when a setting in a Go service's config seems to have no effect, when reviewing a change to a config file or to a config struct, before deploying a new config, and when adding a config check to CI.
---

# Checking config keys against Go structs

straykeys loads the Go package that declares the config struct (with `go list` and `go/types`, no build), works out which keys the decoder accepts, and checks one or more config files against them. Unknown keys are reported with a likely fix. It reads files only: it doesn't run the program and needs no network once the module's dependencies are downloaded. It needs Go 1.22 or newer.

## Find the inputs

1. **The struct.** Find where the program decodes its config: search for `yaml.Unmarshal`, `yaml.NewDecoder`, `json.Unmarshal`, `json.NewDecoder`, `viper.Unmarshal`, `k.Unmarshal` (koanf), `sigsyaml.Unmarshal`. The struct passed there is `--type`, written as the package path and the type name: `./internal/config.Config`, or `Config` for the package in `--dir`.
2. **The decoder.** `--decoder yaml` for gopkg.in/yaml.v3 (also go.yaml.in/yaml/v3), `json` for encoding/json, sigs.k8s.io/yaml and ghodss/yaml (they use json tags), `viper` for viper.Unmarshal, `koanf` for koanf.Unmarshal. Without the flag it is guessed from the struct tags; pass it when the struct has several kinds of tags.
3. **The config files.** Checked-in configs, examples in docs, Helm or Kustomize output, and ConfigMaps. For a ConfigMap, pass the manifest and `--configmap-key config.yaml` (the data key that holds the file). For a config whose program reads only a section, pass `--at section`.

## Run it

Build it once from the plugin, then run it from the user's module:

    (cd "${CLAUDE_PLUGIN_ROOT}" && go build -o "${TMPDIR:-/tmp}/straykeys" ./cmd/straykeys)
    "${TMPDIR:-/tmp}/straykeys" --dir . --type ./internal/config.Config config/*.yaml

The plugin includes its one dependency (gopkg.in/yaml.v3) in `vendor/`, so the build needs no network. The user's module must have its own dependencies available (`go mod download`, or a `vendor/` directory), because straykeys runs `go list` there. Add `--format json` for machine-readable output, `--allow key.path` for keys the user says are read some other way, and `-` to read a config from stdin.

## Read the exit code first

- **0:** no findings. The last line is `No problems: all N keys in ... match fields of T.` Check that N is about the number of keys in the file.
- **1:** findings (unless `--fail-on never`).
- **2:** nothing was reported, and the message says why: a config that is empty, not YAML or JSON, cut off (JSON), a top level that isn't a mapping, a file none of whose top-level keys belong to the struct (the wrong file, the wrong `--type`, or a config nested under a key), or a Go package that doesn't load or type-check, a missing type, or a type that decodes itself. Fix the input and run again. Never describe exit 2 as a clean result.

## Check each finding

- **ERROR `unknown-key`:** the key matches no field, so the program ignores it and uses the default. The fix names the likely field (`did you mean`), or a place where the key does belong (`move it there`, for a missing level such as `client:`).
- **ERROR `wrong-case`:** yaml.v3 matches keys exactly; `maxConns` doesn't fill a field whose key is `maxconns`.
- **ERROR `duration-without-unit`:** a number for a `time.Duration`. yaml.v3 refuses it; encoding/json, viper and koanf read it as nanoseconds, so `timeout: 30` is 30ns.
- **ERROR `wrong-type`, `wrong-shape`:** the decoder refuses the value, so the program fails at startup.
- **ERROR `duplicate-key`:** a key twice in one mapping. YAML decoders refuse it; encoding/json keeps the last one silently. With case-insensitive decoders, `Port` and `port` fill one field.
- **ERROR `extra-document`:** a second YAML document after `---`, which decoders never read.
- **ERROR `alias-cycle`:** a YAML alias inside the node it points to.
- **ERROR `no-final-newline`:** a YAML file without a final line break may be cut off; the rest of the result still holds.

## Report

1. Each ignored key, the file and line, and the fix. Say what the program uses instead (the field's default).
2. Values the decoder refuses, as startup failures.
3. What was not checked: values that decode themselves (the summary counts them), keys read with `viper.Get` on a `*viper.Viper` value rather than the package functions, and keys read outside the struct.

## Don't

Don't call a config correct because straykeys found nothing. It checks that keys match fields and that values have a type the decoder accepts. It doesn't check that the values are right or that required keys are present. A YAML file cut exactly at a line break looks complete. Don't delete keys the user didn't ask about; some programs read keys outside the struct, which `--allow` accepts.
