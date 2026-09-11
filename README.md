# dockerizethis

[![CI](https://github.com/kaarude/dockerizethis/actions/workflows/ci.yml/badge.svg)](https://github.com/kaarude/dockerizethis/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/kaarude/dockerizethis.svg)](https://pkg.go.dev/github.com/kaarude/dockerizethis)
[![Go version](https://img.shields.io/github/go-mod/go-version/kaarude/dockerizethis)](go.mod)
[![Release](https://img.shields.io/github/v/release/kaarude/dockerizethis?include_prereleases)](https://github.com/kaarude/dockerizethis/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

dockerizethis is being built to detect how a project runs and generate Docker
artifacts. Node detection, Dockerfile templates, and file emission are available
as internal packages. The CLI is still a scaffold: it parses flags and reports
that the pipeline is not implemented. It does not generate or verify files yet.

## Quickstart

Build from a checkout with the Go version declared in `go.mod`:

```sh
git clone https://github.com/kaarude/dockerizethis.git
cd dockerizethis
go build -o dockerizethis ./cmd/dockerizethis
./dockerizethis --help
./dockerizethis ./my-app --dry-run
```

The last command currently reports that no files were generated or verified.
The path defaults to the current directory.

`go install` through the GitHub repository path is not supported yet because
`go.mod` still declares `github.com/carl/dockerizethis`.

## Supported stacks

The Node packages are implemented; connecting them to the CLI is still pending.

| Stack | Detection markers | Status |
| --- | --- | --- |
| Node.js / TypeScript | `package.json`, lockfiles | Internal packages; CLI pending |
| Go | `go.mod` | Roadmap |
| Python | `pyproject.toml`, `requirements.txt` | Roadmap |
| Static site | `index.html` | Roadmap |

The planned artifact set includes `Dockerfile`, `.dockerignore`,
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

The flags above reserve the intended behavior. Currently only argument parsing,
verification-level validation, and text/JSON scaffold output are implemented.

## How it works

The planned CLI pipeline will detect the stack, complete a plan, emit artifacts, verify
the build, then report what happened. Each stage is a package under `internal/`, and the
interfaces between them are frozen so the stages can be built and tested on their own.
See [CONTRIBUTING.md](CONTRIBUTING.md) to add a stack.

## Contributing

Bug reports, stack requests, and pull requests are welcome. Start with
[CONTRIBUTING.md](CONTRIBUTING.md). For security issues, see [SECURITY.md](SECURITY.md).

## License

MIT. See [LICENSE](LICENSE).
