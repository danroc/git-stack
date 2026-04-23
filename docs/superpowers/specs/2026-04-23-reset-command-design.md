# Design: `git-stack reset` Command

## Overview

Add a `reset` command that removes all git-stack state (`branch.*.stackParent` and `branch.*.stackParentMergeBase`) from the local git config across all branches.

## Command Interface

```
git-stack reset
```

- No arguments, no flags
- Removes all `stackParent` and `stackParentMergeBase` config entries for every branch
- Prints `Removing stack config for <branch>` to stdout for each affected branch
- Exits 0 on success
- If no stack config exists, prints nothing and exits 0

## Implementation

### `git.Client.ResetStackConfig()` — `pkg/git/git.go`

New method with signature `ResetStackConfig() ([]string, error)` that:

1. Runs `git config --local --list` to fetch all local config
2. Parses output using the existing `parseBranchConfigKey` helper to identify `stackParent` and `stackParentMergeBase` keys
3. Collects the unique set of branch names that have either key configured
4. For each branch, unsets both `branch.<name>.stackParent` and `branch.<name>.stackParentMergeBase` via `git config --local --unset` (ignoring exit code 1, which means the key was already absent)
5. Returns the sorted list of affected branch names

### `cmdReset()` — `cmd/git-stack/main.go`

New command function that:

1. Creates a `git.Client` for the current directory
2. Calls `ResetStackConfig()`
3. Prints `Removing stack config for <branch>` for each returned branch name
4. Returns any error from the method
5. Wired into `root.AddCommand()` alongside existing commands

### Testing

- **Unit test** (in `pkg/git/git_test.go`): create a repo with stack config on 2+ branches, call `ResetStackConfig()`, verify all entries are removed and the correct sorted branch list is returned
- **Unit test**: call on a repo with no stack config, verify empty slice and nil error
- **Integration test**: shell script that creates branches with stack config, runs `git-stack reset`, and verifies `.git/config` no longer contains stack entries
