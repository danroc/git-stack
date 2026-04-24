# CI Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a GitHub Actions workflow that runs lint, build, and tests on PRs and pushes to `main`.

**Architecture:** A single workflow file at `.github/workflows/build-test-lint.yml` with one job (`build-test-lint`) running on `ubuntu-latest`. The job checks out the repo, installs Go 1.26.2 and golangci-lint, then runs `make lint`, `make build`, and `make test` in sequence, finishing with a clean working tree check.

**Tech Stack:** GitHub Actions, Go 1.26.2, golangci-lint v2

---

### Task 1: Create the CI workflow file

**Files:**
- Create: `.github/workflows/build-test-lint.yml`

- [ ] **Step 1: Create the `.github/workflows/` directory**

Run:
```bash
mkdir -p /Users/daniel/code/github.com/danroc/git-stack/.github/workflows
```

- [ ] **Step 2: Write the workflow file**

Create `.github/workflows/build-test-lint.yml` with this exact content:

```yaml
name: Build, test and lint

permissions:
  contents: read

on:
  push:
    branches: ['main']
  pull_request:
    branches: ['main']

jobs:
  build-test-lint:
    runs-on: ubuntu-latest

    steps:
      - name: Checkout repository
        uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6
        with:
          fetch-depth: 0

      - name: Install Go
        uses: actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c # v6
        with:
          go-version: '1.26.2'
          cache: true

      - name: Install golangci-lint
        uses: golangci/golangci-lint-action@1e7e51e771db61008b38414a730f564565cf7c20 # v9
        with:
          version: v2.11.4
          install-only: true

      - name: Lint
        run: make lint

      - name: Build
        run: make build

      - name: Run tests
        run: make test

      - name: Require clean working directory
        shell: bash
        run: |
          if ! git diff --exit-code; then
            echo "Working tree dirty at end of job"
            exit 1
          fi
```

Key differences from the geoblock example:
- No `make tools` step (not needed for CI)
- No shellcheck step (no shell scripts in this project)
- No codecov upload (not using codecov)
- No separate e2e/integration test steps (no such tests exist yet)
- No `install-only: true` alternative — we install golangci-lint via the action so `make lint-golangci` can find it

- [ ] **Step 3: Verify the file was created correctly**

Run:
```bash
cat /Users/daniel/code/github.com/danroc/git-stack/.github/workflows/build-test-lint.yml
```

Expected: The file content matches exactly what was written in Step 2.

- [ ] **Step 4: Commit**

Run:
```bash
git add .github/workflows/build-test-lint.yml
git commit -m "ci: add build, test and lint workflow"
```
