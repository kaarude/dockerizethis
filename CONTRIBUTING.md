# Contributing to dockerizethis

Thanks for helping. This guide covers local setup, the checks we run, and how to add a
new stack.

## Development setup

You need Go. The exact version is in `go.mod`; install that or newer. The CLI depends on
Cobra and Testify, and on the standard library for everything else.

```sh
git clone https://github.com/kaarude/dockerizethis.git
cd dockerizethis
go build ./...
go test ./...
```

There are no services or code generators to start. Docker is only needed if you want to
exercise the verification step end to end.

## Checks before a change

Run the same commands CI runs — `scripts/test.sh` covers all of them:

```sh
scripts/test.sh
```

which is `go vet ./...` plus `go test ./...`, plus `golangci-lint run` when the
binary is installed. `scripts/e2e.sh` additionally builds the CLI and runs it
against every fixture under `testdata/fixtures/`.

For CLI changes, also confirm the help text and the command path you touched:

```sh
go run ./cmd/dockerize --help
go run ./cmd/dockerize ./some-project
```

Keep test coverage focused on observable behavior and failure cases. A test that pins a
constant or restates an implementation detail will slow future changes without catching
anything.

## Project layout

The pipeline runs in one direction and each stage lives in its own package:

- `internal/detect` finds the stack and returns a `plan.Plan`.
- `internal/plan` holds the plan type and its validation rules.
- `internal/emit` writes artifacts to disk.
- verification builds the image and, at the `full` level, checks the container starts.
- reporting collects the results for the CLI.

The declarations in `internal/plan/plan.go`, `internal/detect/detect.go`, and
`internal/emit/emit.go` are frozen. Changing their names, types, fields, JSON tags,
constant values, method signatures, or documented behavior needs approval from a
maintainer first. Implement against them as they are.

Do not edit `go.mod` by hand or run `go get` or `go mod tidy`. Cobra and Testify are the
only allowed direct dependencies. If you need another library, open an issue instead.

## Adding a stack

A stack is a detector plus templates plus a fixture. To add one:

1. Implement `detect.Detector` in `internal/detect`. `Name()` returns the stack name, and
   `Detect(dir)` returns `ok=false` when the stack's markers are absent. Fill in a
   `plan.Plan` with the runtime version, package manager, framework, process type, port,
   health path, build and start commands, env vars, and any backing services. Keep
   detection read-only and never error on a project that simply does not match.
2. Register the detector in the detector list the CLI walks.
3. Add templates for the stack-specific artifacts: `Dockerfile` and
   `.dockerignore`. Templates live beside the code that renders them under
   `internal/templates/<stack>/`. Stack-independent artifacts —
   `docker-compose.yml`, the GHCR workflow, `DEPLOY.md`, and `.env.example` —
   come from `internal/templates/common` and `internal/emit`.
4. Add a fixture project under `testdata/fixtures/`. Keep it small: the marker
   files and just enough source for detection to be realistic. The e2e script
   and the docker-build workflow pick it up automatically.
5. Add a golden test. Run detect and emit against the fixture into a temp directory, then
   compare each artifact with the file under `testdata/golden/`. Add an update flag
   (commonly `-update`) so a maintainer can regenerate goldens after an intended change.

Env vars in a plan carry a name, whether they are required, and a hint. Never put a
secret value in a plan or a template. `.env.example` gets the variable name and the hint
only.

## Pull requests

Keep a pull request focused on one change. Describe what you changed and how you verified
it, and include the commands you ran. If your change touches a stack, say which fixture
and golden you added. Do not commit generated binaries or local artifacts.

By contributing you agree that your work is licensed under the MIT license in
[LICENSE](LICENSE).
