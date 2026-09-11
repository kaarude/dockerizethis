# Working on dockerizethis

## Frozen contracts

The declarations in these files are FROZEN:

- `internal/plan/plan.go`
- `internal/detect/detect.go`
- `internal/emit/emit.go`

Changes to their names, types, fields, JSON tags, constant values, method signatures,
or documented behavior require the human's approval. Implement features against
these contracts. The human approved the initial error-returning `emit.Write` stub;
its body may be implemented without changing the contract. Preserve the supplied
contract formatting when formatting other Go files.

## Dependencies

Do not edit `go.mod`. Cobra and Testify are the only allowed direct dependencies;
their required indirect dependencies are already recorded. Use the standard library
for everything else. Avoid commands that change module requirements, including
`go get` and `go mod tidy`. Bring any dependency blocker to the human.

## Tests and verification

Every feature gets focused tests for its observable behavior and failure cases.
Keep tests beside the code they exercise. Before handing off work, run:

```sh
go build ./... && go test ./...
```

For CLI changes, also exercise `go run ./cmd/dockerizethis --help` and the affected
command path. Never report artifacts as verified unless verification actually ran.

## File ownership for parallel agents

Before parallel work begins, assign each agent an explicit, disjoint set of files
or directories, including test files. Each file has one writer at a time. Read
shared contracts freely; coordinate changes outside your ownership with the owner
before editing. Hand off shared CLI wiring to a designated integration owner.
Preserve other agents' edits and report changed files and verification results at
handoff. The integration owner runs the full build and test commands after combining
the changes. Never commit or push without the human's instruction.

## CLI integration conventions

- Node detection sets `Extras["lockfile"]="none"` when no lockfile exists;
  the renderer then uses `npm install`.
- Use `verify.Run` for the complete verification sequence. Image handoff and
  container cleanup belong inside verification, not in the detection plan.
- Keep stdout parseable under `--json`. Prompts, progress, and emitter dry-run
  diffs belong on stderr.
