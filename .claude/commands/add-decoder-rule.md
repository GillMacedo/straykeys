Teach straykeys a decoder rule: $ARGUMENTS

1. Show the rule with the real library first: add a struct to `tests/decoders/types.go` and a fixture to `tests/decoders/testdata/`, add a suite to `suites` in `tests/decoders/decoders_test.go`, and run `make test-decoders` to see where straykeys disagrees. Link the library source behind the rule in the PR.
2. Change `cmd/straykeys/schema.go` (which keys a struct accepts) or `cmd/straykeys/check.go` (how values are checked) until the suite passes with several seeds: `STRAYKEYS_MUTANTS=400 STRAYKEYS_SEED=3 make test-decoders`.
3. Update the decoder table in `README.md` and the skill in `skills/checking-config-keys-against-go-structs/SKILL.md`.
4. Run `make test`.
