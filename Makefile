.PHONY: build test vet lint docs-lint schema schema-write third-party third-party-check version-check
.PHONY: fmt fmt-check lint-go lint-exceptions lint-exceptions-update lint-sh lint-actions lint-yaml vuln deps-outdated

# Pinned in tools/go.mod; shellcheck-py and yamllint are pinned here.
TOOL = go tool -modfile=tools/go.mod
# actionlint has its own module: its yaml/v4 pin conflicts with golangci-lint's gosec.
ACTIONLINT = go tool -modfile=tools/actionlint/go.mod
SH_FILES = scripts/dev/bin/duolog scripts/dev/rebuild scripts/compat/smoke.sh
SHELLCHECK_PY = 0.11.0.1
YAMLLINT = 1.38.0

build:
	go build -o .cache/bin/duolog ./cmd/duolog

test:
	go test -timeout 10m ./...

vet:
	go vet ./...

# Fails when gofmt would change any Go file and lists those files.
lint:
	@files=$$(gofmt -l .); if [ -n "$$files" ]; then echo "gofmt needed:" >&2; echo "$$files" >&2; exit 1; fi

# The formatters have their own config, so that lint-go does not report them.
fmt:
	$(TOOL) golangci-lint fmt -c .golangci.fmt.yml
	$(TOOL) shfmt -p -w $(SH_FILES)

fmt-check:
	$(TOOL) golangci-lint fmt -c .golangci.fmt.yml --diff
	$(TOOL) shfmt -p -d $(SH_FILES)

# GOOS=windows make lint-go lints the Windows files from any host: the tool is
# built for the host, and GOOS only reaches the packages it analyses.
lint-go:
	@bin=$$(env -u GOOS -u GOARCH $(TOOL) -n golangci-lint) && $$bin run

# Runs lint-go for linux, darwin and windows without the size exceptions and
# fails when a listed exception no longer fires.
lint-exceptions:
	uv run scripts/lint/exceptions.py check

# Rewrites the exceptions after a function or file shrank; it refuses to add one.
lint-exceptions-update:
	uv run scripts/lint/exceptions.py update

lint-sh: install-script
	uvx --from shellcheck-py==$(SHELLCHECK_PY) shellcheck $(SH_FILES) .release/install.sh

lint-actions:
	$(ACTIONLINT) actionlint

lint-yaml:
	uvx --from yamllint==$(YAMLLINT) yamllint .

vuln:
	$(TOOL) govulncheck ./...

deps-outdated:
	go list -m -u -f '{{if and .Update (not .Main)}}{{.Path}} {{.Version}} -> {{.Update.Version}}{{end}}' all

version-check:
	uv run scripts/version/version.py check

docs-lint:
	@find README.md docs -path docs/superpowers -prune -o -type f -name '*.md' -print0 | xargs -0 uvx --from pymarkdownlnt==0.9.41 pymarkdown -c .pymarkdown.yaml scan
	go test -run TestDocs .

schema:
	@go test . -run '^TestSchemaFileIsCurrent$$' > /dev/null || { echo 'Schema check failed; run make schema-write' >&2; exit 1; }

schema-write:
	@mkdir -p .cache
	go run ./scripts/schemagen > .cache/schema.json.tmp
	mv .cache/schema.json.tmp duolog-config.schema.json

third-party:
	go run ./scripts/thirdparty

third-party-check:
	go run ./scripts/thirdparty -check

# The README shows assets/how-duolog-works.svg as two WebP images, light and
# dark, because a picture element picks one by the reader's theme.
# rsvg-convert ignores @media, so the dark image drops the line that opens the
# SVG's dark-theme block and the line that closes it, a "}" indented by four
# spaces. Keep that block so. Needs rsvg-convert and cwebp.
.PHONY: diagram
diagram:
	@mkdir -p .cache
	rsvg-convert -w 2000 assets/how-duolog-works.svg -o .cache/diagram-light.png
	sed -e '/prefers-color-scheme: dark/d' -e '/^    }$$/d' assets/how-duolog-works.svg | rsvg-convert -w 2000 -o .cache/diagram-dark.png
	cwebp -quiet -q 90 .cache/diagram-light.png -o assets/how-duolog-works-light.webp
	cwebp -quiet -q 90 .cache/diagram-dark.png -o assets/how-duolog-works-dark.webp

.PHONY: install-script
install-script:
	go run ./scripts/render-install -version $(or $(RELEASE_TAG),latest)
