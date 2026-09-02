GO      ?= go
NPM     ?= npm
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

SQLC_VERSION ?= v1.31.1

.PHONY: check fmtcheck build web run test fmt tidy clean docker sqlc changelog

## check: the gate every milestone must pass
##
## It runs what CI runs, in CI's order. It used to skip gofmt, which CI
## enforces as its own step — so `make check` passed locally and CI failed on
## a stray blank line. A local gate that does not match the remote one is
## worse than no local gate, because it is trusted.
check: fmtcheck
	$(GO) build ./...
	$(GO) vet ./...
	$(GO) test ./...
	$(NPM) --prefix web run build

## fmtcheck: fail if anything needs gofmt (CI runs this same command)
fmtcheck:
	@unformatted=$$(gofmt -l ./cmd ./internal); \
	if [ -n "$$unformatted" ]; then \
		echo "These files need gofmt:"; \
		echo "$$unformatted"; \
		echo "Run: make fmt"; \
		exit 1; \
	fi

## web: build the frontend into the Go embed directory
web:
	$(NPM) --prefix web ci
	$(NPM) --prefix web run build

## build: full binary with the UI embedded
build: web
	$(GO) build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o silt ./cmd/silt

## run: run from source (frontend must already be built, or the UI 503s)
run:
	$(GO) run ./cmd/silt

test:
	$(GO) test ./...

race:
	$(GO) test ./... -race -count=1

## changelog: regenerate CHANGELOG.md from internal/changelog
changelog:
	$(GO) run ./internal/changelog/cmd/gen CHANGELOG.md

## sqlc: regenerate the typed query layer from internal/store/queries
sqlc:
	$(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

## clean: remove build output but keep the .gitkeep that //go:embed needs
clean:
	rm -f silt
	find internal/web/dist -mindepth 1 ! -name .gitkeep -delete
	rm -rf web/dist

docker:
	docker buildx build --platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) -t silt:$(VERSION) .
