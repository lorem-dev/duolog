.PHONY: build test vet docs-lint schema schema-write

build:
	go build ./cmd/duolog

test:
	go test ./...

vet:
	go vet ./...

docs-lint:
	@find README.md docs -path docs/superpowers -prune -o -type f -name '*.md' -print0 | xargs -0 uvx --from pymarkdownlnt pymarkdown -c .pymarkdown.yaml scan
	go test -run TestDocs .

schema:
	@go test ./internal/config/ -run '^TestSchemaFileIsCurrent$$' > /dev/null || { echo 'Schema check failed; run make schema-write' >&2; exit 1; }

schema-write:
	go run ./cmd/duolog config schema > duolog-config.schema.json
