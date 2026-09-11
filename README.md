# dockerizethis

[![CI](https://github.com/kaarude/dockerizethis/actions/workflows/ci.yml/badge.svg)](https://github.com/kaarude/dockerizethis/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/carl/dockerizethis.svg)](https://pkg.go.dev/github.com/carl/dockerizethis)
[![Go version](https://img.shields.io/github/go-mod/go-version/carl/dockerizethis)](go.mod)
[![Release](https://img.shields.io/github/v/release/kaarude/dockerizethis?include_prereleases)](https://github.com/kaarude/dockerizethis/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

dockerize detects how your project runs and generates the Docker artifacts
you own — `Dockerfile`, `.dockerignore`, `docker-compose.yml`, `.env.example`,
a GHCR publishing workflow, and `DEPLOY.md` — then proves they build.

## Install

```sh
go install github.com/carl/dockerizethis/cmd/dockerize@latest
```

Or build from a checkout with the Go version declared in `go.mod`:

```sh
git clone https://github.com/kaarude/dockerizethis.git
cd dockerizethis
go build -o dockerize ./cmd/dockerize
```

## Quickstart

```sh
dockerize ./my-app
```

Point it at any project directory (the path defaults to `.`). It detects the
stack, proposes a plan, writes the artifacts, and verifies the image builds —
use `--verify=none` to skip the build or `--dry-run` to preview without
writing.

## Supported stacks

| Stack | Detection markers | Status |
| --- | --- | --- |
| Node.js / TypeScript | `package.json`, lockfiles | Supported |
| Go | `go.mod` | Supported |
| Python | `pyproject.toml`, `requirements.txt` | Supported |
| Rust | `Cargo.toml`, `rust-toolchain.toml` | Supported |
| Java / Kotlin | `pom.xml`, `build.gradle`, `mvnw`/`gradlew` | Supported |
| .NET (C# / F#) | `*.csproj`, `*.fsproj`, `*.sln` | Supported |

Generated compose files can wire up Postgres, Redis, MySQL, and Mongo backing
services with healthchecks and named volumes.

## Flags

```
dockerize [path] [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `[path]` | argument | `.` | Project directory to inspect |
| `--dry-run` | bool | `false` | Preview artifacts without writing files |
| `--yes` | bool | `false` | Accept prompts without interaction |
| `--json` | bool | `false` | Print the report as JSON |
| `--verify` | string | `build` | Verification level: `none`, `build`, or `full` |
| `--stack` | string | `""` | Override the detected stack |
| `--service` | string | `""` | Select a service subdirectory in a monorepo |
| `--force` | bool | `false` | Allow replacing existing artifact files |
| `--backup` | bool | `false` | Back up existing artifact files before replacement |
| `--help`, `-h` | bool | `false` | Show help for the command |

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Usage or infrastructure error |
| 2 | `docker build` failed |
| 3 | Smoke test failed (`--verify=full`) |
| 4 | Docker CLI or daemon unavailable |

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
