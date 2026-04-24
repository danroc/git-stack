# CI Workflow Design

## Goal
Add a GitHub Actions workflow to run lint, build, and tests on every pull request and push to `main`.

## Approach

A single job (`build-test-lint`) on `ubuntu-latest` that runs sequentially:

1. **Checkout** with `fetch-depth: 0` for full history
2. **Install Go** pinned to `1.26.2`
3. **Install golangci-lint** via the official `golangci/golangci-lint-action`
4. **Lint** — `make lint` (runs `go mod tidy`, `golangci-lint fmt`, `golangci-lint run`)
5. **Build** — `make build`
6. **Test** — `make test` (`go test ./...`)
7. **Verify clean working tree** — fail if `make lint` left unstaged changes (catches dirty `go.mod`/`go.sum` or unformatted files)

## Workflow Triggers

- `push` to `main`
- `pull_request` to `main`

## Decisions

- **Minimal scope**: lint + build + tests only. No shellcheck (no `.sh` files), no codecov.
- **Tidy check**: `make lint` runs `go mod tidy` — the clean working tree check at the end catches any changes to `go.mod`/`go.sum`.
- **Go version**: Pinned to `1.26.2` (matches `go.mod`).
- **Single job**: No need for parallelism — lint, build, and tests are fast on a Go project of this size.

## File

- **Create**: `.github/workflows/build-test-lint.yml`
