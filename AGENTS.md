# Agent Guidelines

Behavioral guidelines for coding agents in this repo, following Andrej Karpathy's
guidelines for LLM coding. Keep changes minimal and verifiable.

## Think before coding

- State assumptions. Ask when uncertain.
- If multiple approaches exist, say so — don't pick silently.
- Push back when a simpler approach exists.

## Simplicity first

- Only build what was asked for.
- No speculative abstractions, config, or error handling.
- If it feels overcomplicated, simplify.

## Surgical changes

- Touch only what the task requires.
- Match existing style and conventions.
- Remove dead code only if your changes created it. Mention pre-existing dead code; don't delete it unless asked.

## Goal-driven execution

- Define verifiable success criteria before implementing.
- For bugs: reproduce, then fix.
- For refactors: tests pass before and after.

## Before committing

The agent **MUST** run these commands from the repo root and confirm they all pass
before creating a commit:

```bash
make build
make lint
make test
```

If a format command modifies files, stage those changes before committing.
