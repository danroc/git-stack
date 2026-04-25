# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.1] - 2026-04-25

### Fixed

- `push` now uses `--force-with-lease` so rebased stacks can be pushed without errors

## [0.1.0] - 2026-04-24

### Added

- Initial release with full stack management CLI:
  - `add`, `view`, `rebase`, `push`, `pull`, `move`, `parent`, `reset`, `version`
  - Graph-first stack discovery with `stackParent` config tiebreak
  - Cascading rebase when reparenting branches
  - Color tree view output
  - Worktree support
  - `--base <branch>` flag on all commands

[Unreleased]: https://github.com/danroc/git-stack/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/danroc/git-stack/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/danroc/git-stack/releases/tag/v0.1.0
