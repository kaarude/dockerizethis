# dockerizethis

[![CI](https://github.com/kaarude/dockerizethis/actions/workflows/ci.yml/badge.svg)](https://github.com/kaarude/dockerizethis/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/carl/dockerizethis.svg)](https://pkg.go.dev/github.com/carl/dockerizethis)
[![Go version](https://img.shields.io/github/go-mod/go-version/carl/dockerizethis)](go.mod)
[![Release](https://img.shields.io/github/v/release/kaarude/dockerizethis?include_prereleases)](https://github.com/kaarude/dockerizethis/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

dockerizethis detects Node.js, Go, and Python apps and generates `Dockerfile`,
`.dockerignore`, `docker-compose.yml`, a GHCR publishing workflow, and
`DEPLOY.md`. Projects with environment variables also get `.env.example`.
By default it builds the image with Docker.

## Install

Build from a checkout with the Go version declared in `go.mod`:

```sh
git clone https://github.com/kaarude/dockerizethis.git
cd dockerizethis
go build -o dockerizethis ./cmd/dockerizethis
./dockerizethis --help
```

The commands below assume the binary is on your `PATH`. When using the checkout,
replace `dockerizethis` with the path to that binary.

## Quickstart

```sh
# Inspect the proposed files. This never writes or calls Docker.
dockerizethis ./my-app --dry-run

# Generate artifacts and build the image. Requires a running Docker daemon.
dockerizethis ./my-app --yes

# Generate artifacts without Docker.
dockerizethis ./my-app --yes --verify=none
```

The path defaults to `.`. With terminal output, writing requires a `y` or `yes`
confirmation unless you pass `--yes`. Piped output runs without a prompt.
Existing files are skipped. `--force` replaces them; `--backup` preserves each
original as `<filename>.bak` before replacing it. An existing backup causes an
error. Earlier files may already have been written when a later file fails.

Example output for the `go-http` fixture copied to `/work/my-app`, using
`dockerizethis /work/my-app --yes --verify=none`:

```text
Project: /work/my-app
Detected: go 1.23 | framework: net/http | process: web
Port: 8080 | services: none | confidence: 90%
  created Dockerfile
  created .dockerignore
  created docker-compose.yml
  created .env.example
  created .github/workflows/docker.yml
  created DEPLOY.md
Verification: skipped (--verify=none)
Configure .env from .env.example before starting:
  PORT (optional; omit to keep the app default)
Next: in /work/my-app, review DEPLOY.md and run docker compose up -d --build.
```

Before starting, review the generated files. If `.env.example` exists, copy it
to `.env`, fill the required values, and remove optional assignments you do not
need. Values in `.env.example` are deliberately empty. Database passwords used
by Compose are included, and app connection URLs must use the Compose service
hostname and matching credentials.

```sh
cd my-app
cp .env.example .env  # only when the template exists; edit before continuing
docker compose up -d --build
```

## Verification and reports

`--verify=build`, the default, runs `docker build` and leaves a uniquely tagged
local image whose name appears in the report. It does not prove the app starts.
`--verify=full` also starts a temporary container for web processes, passes an
existing `.env` through Docker's `--env-file`, and probes HTTP on a random
loopback port. It removes the temporary container even when the probe fails.
Full verification requires a local Docker daemon and runtime configuration the
app needs. It does not start Compose or backing services.

When detection supplies a health path, a 2xx or 3xx response passes. Otherwise,
the probe uses `/` and accepts any non-5xx response, including 404, as evidence
that the HTTP server started. Workers and static sites receive build verification
only. `--verify=none` skips verification; `--dry-run` always skips it regardless
of the selected level.

Exit codes are `0` for success or a declined write prompt, `1` for argument,
detection, rendering, write errors, or cancellation, `2` for build failure, `3` for smoke
failure, and `4` when Docker is unavailable.

Verification builds once and uses that exact image for the startup probe. Build
output retains the last 64 KiB. Recognized failures include the failed Dockerfile
step and a remediation hint in the report.

`--json` writes one object with `plan`, `results`, and `verify` to stdout.
Verification results include build output or the HTTP probe result when run.
Prompts, progress, dry-run diffs, and error diagnostics go to stderr:

```sh
dockerizethis ./my-app --dry-run --json > report.json
```

## Supported stacks

| Stack | Detection markers | Status |
| --- | --- | --- |
| Node.js / TypeScript | `package.json`, lockfiles | Supported |
| Go | `go.mod` | Supported |
| Python | `pyproject.toml`, `requirements.txt`, `setup.py` | Supported |

Generated Compose files include Postgres, Redis, MySQL, and Mongo when detected.
Postgres, MySQL, and Mongo use named volumes; Redis is ephemeral by default.
Detection is heuristic. Read plan notes and confirm the start command, port,
and environment before deploying. Node projects without a lockfile use
`npm install` during the image build; committing a lockfile makes later builds
reproducible.

All three detectors run. The highest confidence wins, with ties resolved in
Node, Go, Python order. `--stack=node|go|python` selects a stack only if it was
detected. Detector read or parse errors are reported instead of silently ignored.

`--service=services/api` inspects and writes inside that subdirectory. For a
monorepo, move the generated workflow to the repository's root
`.github/workflows/` and change its build context to the service directory.
GitHub does not discover workflows nested inside service directories.

## Flags

```
dockerizethis [path] [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `[path]` | argument | `.` | Project directory to inspect |
| `--dry-run` | bool | `false` | Preview artifacts without writing files |
| `--yes` | bool | `false` | Accept prompts without interaction |
| `--json` | bool | `false` | Print the report as JSON |
| `--verify` | string | `build` | Verification level: `none`, `build`, or `full` |
| `--stack` | string | `""` | Select a detected stack: `node`, `go`, or `python` |
| `--service` | string | `""` | Select a service subdirectory in a monorepo |
| `--force` | bool | `false` | Allow replacing existing artifact files |
| `--backup` | bool | `false` | Back up existing artifact files before replacement |
| `--help`, `-h` | bool | `false` | Show help for the command |

## How it works

The pipeline detects the stack, completes a plan, emits artifacts, verifies the
build, then reports what happened. Each stage is a package under `internal/`,
and the interfaces between them are frozen so stages can be built and tested on
their own. See [CONTRIBUTING.md](CONTRIBUTING.md) to add a stack.

## Contributing

Bug reports, stack requests, and pull requests are welcome. Start with
[CONTRIBUTING.md](CONTRIBUTING.md). For security issues, see [SECURITY.md](SECURITY.md).

## License

MIT. See [LICENSE](LICENSE).
