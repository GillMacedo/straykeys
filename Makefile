.PHONY: test test-decoders lint demo

test:
	test -z "$$(gofmt -l cmd tests examples)"
	go vet ./...
	go test -race ./...
	$(MAKE) test-decoders

# Compares straykeys with the real decoders. This module downloads viper,
# koanf and sigs.k8s.io/yaml the first time it runs.
# vet's structtag check is off because the test types repeat json tags on
# purpose, to test encoding/json's rules for conflicting embedded fields.
test-decoders:
	cd tests/decoders && go vet -structtag=false ./... && go test ./...

# Not --strict: the repository is the plugin (docs/decisions/0002), so the
# validator warns that CLAUDE.md at its root isn't loaded as plugin context.
# The check below fails on any other warning.
lint:
	claude plugin validate . > .lint.out 2>&1; status=$$?; cat .lint.out; \
	other=$$(grep '❯ root:' .lint.out | grep -v 'CLAUDE.md at the plugin root'); rm -f .lint.out; \
	test $$status -eq 0 && test -z "$$other"

# The prod config has stray keys on purpose, so that run exits 1.
demo:
	go run ./cmd/straykeys --dir examples/billing --type Config examples/billing/config.prod.yaml || test $$? -eq 1
	go run ./cmd/straykeys --dir examples/billing --type Config examples/billing/config.dev.yaml
