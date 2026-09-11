# dockerizethis

[![CI](https://github.com/kaarude/dockerizethis/actions/workflows/ci.yml/badge.svg)](https://github.com/kaarude/dockerizethis/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/kaarude/dockerizethis.svg)](https://pkg.go.dev/github.com/kaarude/dockerizethis)
[![Go version](https://img.shields.io/github/go-mod/go-version/kaarude/dockerizethis)](go.mod)
[![Release](https://img.shields.io/github/v/release/kaarude/dockerizethis?include_prereleases)](https://github.com/kaarude/dockerizethis/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

dockerizethis reads a project, works out how it is built and started, and writes the
Docker artifacts you would otherwise hand-roll: a Dockerfile, `.dockerignore`, a compose
file, `.env.example`, a CI workflow, and deploy notes. Then it runs the build to prove
the image actually comes up. Every file lands in your repo, so you own and can edit it.
There is no hosted service and no lock-in.

## Quickstart

Install the CLI:

```sh
go install github.com/kaarude/dockerizethis/cmd/dockerizethis@latest
```

Point it at a project:

```sh
dockerizethis ./my-app
```

The path defaults to the current directory, so `dockerizethis` alone works too. On the
first run dockerizethis prints what it detected, shows the artifacts it plans to write,
and verifies the result with a build. Use `--dry-run` to preview without touching disk.

## Supported stacks

Detection ships stack by stack. Everything below is on the roadmap; Node.js and
TypeScript land first.

| Stack | Detection markers | Status |
| --- | --- | --- |
| Node.js / TypeScript | `package.json`, lockfiles | Roadmap |
| Go | `go.mod` | Roadmap |
| Python | `pyproject.toml`, `requirements.txt` | Roadmap |
| Static site | `index.html` | Roadmap |

Each stack produces the same artifact set: `Dockerfile`, `.dockerignore`,
`compose.yaml`, `.env.example`, a CI workflow, and deploy notes. Plans carry the
runtime version, package manager, framework, process type, port, health path, and any
backing services (Postgres, Redis, MySQL, Mongo) they need.

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
| `--stack` | string | `""` | Override the detected stack |
| `--service` | string | `""` | Select a service subdirectory in a monorepo |
| `--force` | bool | `false` | Allow replacing existing artifact files |
| `--backup` | bool | `false` | Back up existing artifact files before replacement |
| `--help`, `-h` | bool | `false` | Show help for the command |

`--verify=none` skips verification, `--verify=build` builds the image, and
`--verify=full` builds and then checks the container starts and answers its health
path. Verification never runs during `--dry-run`.

## How it works

The CLI runs a fixed pipeline: detect the stack, complete a plan, emit artifacts, verify
the build, then report what happened. Each stage is a package under `internal/`, and the
interfaces between them are frozen so the stages can be built and tested on their own.
See [CONTRIBUTING.md](CONTRIBUTING.md) to add a stack.

## Contributing

Bug reports, stack requests, and pull requests are welcome. Start with
[CONTRIBUTING.md](CONTRIBUTING.md). For security issues, see [SECURITY.md](SECURITY.md).

## License

MIT. See [LICENSE](LICENSE).
