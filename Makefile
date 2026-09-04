GO      ?= go
NPM     ?= npm
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

SQLC_VERSION ?= v1.31.1

.PHONY: check fmtcheck build web run test fmt tidy clean docker sqlc changelog release demo demo-site demo-site-verify e2e

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

## demo: build a populated database, for development without a Docker host
##
##   make demo && SILT_DB_PATH=.demo/silt.db go run ./cmd/silt
demo:
	@rm -rf .demo && mkdir -p .demo
	$(GO) run ./cmd/silt-demo .demo/silt.db

## demo-site: the static demo published to GitHub Pages
##
## No server runs behind the published site, so the UI is built with the demo
## flag set and its /api calls are answered from a file captured here, off a
## real Silt reading the demo database. Nothing about the app is special-cased:
## the same components, the same client, only the transport differs.
##
## SILT_BASE_PATH is the project path Pages serves under; override it for a
## user or custom-domain site, where the base is "/".
DEMO_SITE_DIR  ?= .demo-site
DEMO_BASE_PATH ?= /silt/
DEMO_ADDR      ?= 127.0.0.1:8411

demo-site:
	@rm -rf $(DEMO_SITE_DIR) .demo && mkdir -p $(DEMO_SITE_DIR) .demo
	$(GO) run ./cmd/silt-demo .demo/silt.db
	$(GO) build -o .demo/silt ./cmd/silt
	SILT_BASE_PATH=$(DEMO_BASE_PATH) SILT_WEB_OUT=$(CURDIR)/$(DEMO_SITE_DIR) VITE_SILT_DEMO=1 \
		$(NPM) --prefix web run build
	@echo "capturing fixtures"
	@SILT_DB_PATH=$(CURDIR)/.demo/silt.db SILT_LISTEN_ADDR=$(DEMO_ADDR) \
	  SILT_DOCKER_HOST=tcp://127.0.0.1:1 SILT_LOCAL_ACCOUNT=false SILT_LOG_LEVEL=warn \
	  ./.demo/silt & echo $$! > .demo/pid; \
	for i in $$(seq 1 40); do \
	  curl -sf http://$(DEMO_ADDR)/healthz >/dev/null && break || sleep 0.25; \
	done; \
	node scripts/capture-demo.mjs http://$(DEMO_ADDR) $(DEMO_SITE_DIR)/demo-fixtures.json; \
	status=$$?; kill $$(cat .demo/pid); rm -f .demo/pid; exit $$status
	@# Pages serves 404.html for unknown paths, which is how a deep link into
	@# a client-side route survives a reload without a server to fall back.
	cp $(DEMO_SITE_DIR)/index.html $(DEMO_SITE_DIR)/404.html
	@# Jekyll would otherwise drop Vite's _-prefixed output.
	touch $(DEMO_SITE_DIR)/.nojekyll
	@echo "demo site in $(DEMO_SITE_DIR)"

## demo-site-verify: prove the built demo has an answer for every screen
##
## The demo's failure mode is a blank panel where a request the capture never
## reached should have been, which nothing else notices. Needs the e2e
## Playwright install, so it is a separate target.
demo-site-verify:
	cd e2e && npm ci && npx playwright install --with-deps chromium
	node scripts/verify-demo.mjs $(DEMO_SITE_DIR) $(DEMO_BASE_PATH)

## e2e: end-to-end checks against a real binary and a seeded database
##
## Separate from `check` because it builds the frontend, seeds a database and
## drives a browser — a minute rather than a second. CI runs it as its own job.
e2e: web
	@rm -rf .e2e && mkdir -p .e2e
	$(GO) build -o .e2e/silt ./cmd/silt
	$(GO) run ./cmd/silt-demo .e2e/silt.db
	cd e2e && npm ci && npx playwright install --with-deps chromium
	cd e2e && SILT_E2E_COMMAND='$(E2E_ENV) ../.e2e/silt' npm test

# The demo database, a Docker host that is deliberately unreachable (nothing
# should be collected during a test run), and the ingest token the live-update
# test posts with.
E2E_ENV = SILT_DB_PATH=../.e2e/silt.db SILT_LISTEN_ADDR=127.0.0.1:8410 \
          SILT_DOCKER_HOST=tcp://127.0.0.1:1 SILT_LOCAL_ACCOUNT=false \
          SILT_INGEST_TOKEN=demo SILT_LOG_LEVEL=warn

## release: tag the current release by hand
##
## Not normally needed: merging a version bump to main publishes the release
## on its own, from the same changelog. This exists for cutting one from a
## commit that is not the tip, and for anywhere the automatic path is not
## wanted.
##
## Refuses a dirty tree and a tag that already exists, because both mean the
## release would not be what the changelog says it is.
release:
	@version=$$($(GO) run ./internal/changelog/cmd/gen --version); \
	if [ -z "$$version" ]; then echo "no release in internal/changelog"; exit 1; fi; \
	if [ -n "$$(git status --porcelain)" ]; then \
	  echo "working tree is dirty; commit before tagging v$$version"; exit 1; fi; \
	if git rev-parse "v$$version" >/dev/null 2>&1; then \
	  echo "v$$version already exists"; exit 1; fi; \
	echo "tagging v$$version"; \
	git tag -a "v$$version" -m "Silt v$$version"; \
	git push origin "v$$version"

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
	rm -rf web/dist .demo .demo-site .e2e e2e/test-results

docker:
	docker buildx build --platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) -t silt:$(VERSION) .
