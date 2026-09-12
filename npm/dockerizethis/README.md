# dockerizethis

`dockerize` — detect how a project runs and generate verified Docker hosting
artifacts (`Dockerfile`, `.dockerignore`, `docker-compose.yml`,
`.env.example`, a GHCR publishing workflow, `DEPLOY.md`), then prove the
image builds.

This package ships prebuilt binaries for macOS, Linux, and Windows via
`optionalDependencies` — no Go toolchain required.

## Usage

```sh
npx dockerizethis ./my-app
```

or inside a project that depends on it:

```sh
npm install --save-dev dockerizethis
npx dockerize --dry-run
```

Flags mirror the Go CLI: `--dry-run`, `--yes`, `--json`,
`--verify none|build|full`, `--stack`, `--service`, `--force`, `--backup`.
See `npx dockerizethis --help` or the
[project README](https://github.com/kaarude/dockerizethis#readme).

## Resolution order

1. `DOCKERIZE_BIN` environment variable, if set.
2. The `dockerizethis-<platform>-<arch>` package installed for this machine.
3. A `dockerize` binary found on `PATH` (e.g. from `go install`).

Unsupported platforms print a pointer to `go install` instead of failing
silently.
