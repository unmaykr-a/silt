# Development

```bash
git clone https://github.com/unmaykr-a/silt
cd silt
make check
```

`go build ./...` works on a clean checkout with no npm step: `internal/web/dist/.gitkeep` is
committed so `//go:embed all:dist` compiles before the frontend has ever been built. You get the
binary, serving a page that tells you the UI was not built.

## Requirements

- Go 1.25+
- Node 20+ (for the frontend)
- Nothing else. SQLite is pure Go; `CGO_ENABLED=0`.

## Make targets

| | |
|---|---|
| `make check` | Build, vet, test, and build the frontend. The pre-commit gate. |
| `make test` | Go tests. |
| `make race` | Go tests with the race detector. |
| `make web` | Build the frontend into `internal/web/dist`. |
| `make e2e` | Build a real binary, seed a demo database, drive it with Playwright. |
| `make demo-site` | Build the static demo published to GitHub Pages. |
| `make demo-site-verify` | Prove the built demo has an answer for every screen. |
| `make changelog` | Regenerate `CHANGELOG.md` from `internal/changelog`. |
| `make sqlc` | Regenerate typed queries. |
| `make migrate-new name=...` | New goose migration. |

## Layout

```
cmd/silt/              the binary
cmd/silt-demo/         seeds a demo database
internal/
  api/                 HTTP handlers, SSE hub, auth middleware
  auth/                sessions, OIDC, forward auth, the local account
  collect/             discovery, the event stream, the file watcher, snapshots
  compose/             the project model, normalisation, file capture
  config/              env parsing, validation, setup checks
  diff/                structural diff and classification
  notify/              shoutrrr
  redact/              the keep-list and the HMAC
  store/               migrations, sqlc output, blobs, retention
  web/                 embeds the built frontend
web/                   Svelte 5 + Vite + Tailwind
e2e/                   Playwright
api/openapi.yaml       the API contract
```

## Conventions worth knowing before you send a patch

**The comments explain *why*.** Not what the code does — that is what the code is for. A comment
that says "the reason this is not the obvious thing" earns its place; a comment that restates the
line above it does not.

**No `TODO` stubs in merged code.** If something is out of scope, delete it and say so in the
pull request.

**A design document is not a test.** This project shipped four features that were specified,
sometimes down to the schema and the config surface, and never wired up. The only thing that
caught all four was a test running from the trigger to the stored row. If you add a trigger, add
that test.

**Rune modules get a plain `.ts` beside them.** The vitest config deliberately has no Svelte
plugin, so a `.svelte.ts` cannot be imported by a test. Pure logic goes in a plain module next to
it — `patch.ts` beside `store.svelte.ts`, `clockcore.ts`, `marker.ts` — and that is where the
tests point.

**Watch for the rune effect loop.** State that a subscription or measurement callback reads
synchronously during effect setup becomes a dependency of that effect; writing it re-runs the
effect forever. Use a plain `let` for comparison state.

**Documentation has drift guards.** `internal/config/documented_test.go` reads the `env` tags off
the struct by reflection and fails when a setting is missing from the reference tables or
`.env.example`. The settings search index is checked against the rendered fields. Adding a
setting means the tests tell you where to document it.

## These pages

The wiki lives in [`docs/wiki/`](https://github.com/unmaykr-a/silt/tree/main/docs/wiki) and is
published by `.github/workflows/wiki.yml` on push to `main`. The repository is the source of
truth, so **an edit made in the wiki's web editor is overwritten by the next publish** — send a
pull request against that directory instead.

A test checks that the sidebar links to every page, that every internal link resolves, and that
no page is orphaned.
