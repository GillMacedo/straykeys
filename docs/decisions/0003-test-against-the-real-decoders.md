# 0003: Test the rules against the real decoders

## Context
Every decoder has its own rules for which key fills which field: case, embedded structs, tags, inline maps, dotted keys, and conversions between value types. A rule written from documentation is easy to get wrong, and a wrong rule either hides an ignored key or reports a used one.

## Decision
Keep a separate test module, `tests/decoders`, that imports the real libraries, generates mutated configs, and compares straykeys with what the library actually decodes, value by value. The root module doesn't import those libraries, so the plugin stays small.

## Consequences
`make test` downloads viper, koanf and sigs.k8s.io/yaml for that module. Writing this harness found mistakes in the first version of the rules, including number-to-string conversions in sigs.k8s.io/yaml that stop below promoted fields, yaml.v3 errors on alias cycles only when it decodes them, and koanf not splitting dotted keys.
