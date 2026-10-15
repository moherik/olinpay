# Agent Instructions for openpay.id

## Start here

Before making changes, read:

1. [`README.md`](README.md) for product overview, API, setup, and current features.
2. [`docs/PRD.md`](docs/PRD.md) for design decisions, package boundaries, flows, security expectations, known limitations, and roadmap.

The repository is the source of truth if those documents differ from the code. Update the docs when architecture, public API, configuration, or known limitations change.

## Project boundaries

- Go module: `github.com/moherik/openpay.id`.
- Keep HTTP concerns in `internal/api` / `internal/webhook`, orchestration in `internal/core`, persistence behind `internal/repository`, and gateway-specific behavior in `pkg/provider/<name>`.
- `pkg/provider` is public and must not import `internal` packages.
- Provider adapters must implement the public contract, be safe for concurrent use, register through `provider.Register`, and be blank-imported by `cmd/openpay/main.go` to ship in the binary.
- Preserve merchant scoping, reference idempotency, callback verification, and outbound webhook delivery guarantees.

## Payment-specific caution

For payment changes, account for retries, ambiguous gateway outcomes, duplicate callbacks, status transitions, integer money/currency validation, and multi-instance behavior—not only the happy path. Do not claim live gateway verification unless it was actually performed. Never commit secrets or local `.env` files.

## Working practices

- Inspect `git status` and target files before editing. Treat existing uncommitted changes as user work; do not reset or revert them.
- Keep changes focused and follow surrounding code style.
- Prefer tests using in-memory repositories and `httptest`; live gateway tests must be opt-in and use credentials supplied securely.
- For public contract changes, explain compatibility impact and add regression tests.
- Run `gofmt`, `go build ./...`, `go vet ./...`, and relevant tests. For cross-cutting changes, run `go test ./...` and `go test -race ./...` when practical.
- Summarize files/behavior changed, tests run, and any remaining limitations. Do not describe roadmap items as implemented.

<!-- antislop:start -->
## antislop
For UI, copy, people, mobile layout, or code comments work, read these installed skill files directly (use these paths even if a same-named global skill exists):
- Core filter, always on: `antislop`: `.codex/skills/antislop/SKILL.md`
- UI / visual: `antislop-ui`: `.codex/skills/antislop-ui/SKILL.md`
- Code comments: `antislop-code`: `.codex/skills/antislop-code/SKILL.md`
- Copy & text: `antislop-copywriting`: `.codex/skills/antislop-copywriting/SKILL.md`
- People: `antislop-human`: `.codex/skills/antislop-human/SKILL.md`
- Mobile / responsive: `antislop-layoutmobile`: `.codex/skills/antislop-layoutmobile/SKILL.md`
Before starting, follow the core's "Two Usage Modes" section in strict order: explicit session instruction first, then global preference, then ask. A session instruction always wins. For a resolved mode, say `antislop active: <mode> (session override).` or `antislop active: <mode> (global preference).` once before presenting findings or making edits, using the actual mode and source. Acknowledging the user's request without naming the source does not replace this notice.
Only an explicit choice of antislop during or after selects a session mode. A request to review, audit, or avoid file edits does not select a mode; read the global preference in that case. Another skill's mode does not select antislop's mode.
If the mode is unresolved, ask during/after and end the response; wait for the answer before any UI review, planning, or concept. For read-only tasks, put the active-mode notice only at the start of the final answer, never in progress messages. For editing tasks, announce before the first edit and omit it from the final answer.
To update antislop later: `npx antislop-ai --update`, or run `npx antislop-ai` and pick Overwrite them.
<!-- antislop:end -->
