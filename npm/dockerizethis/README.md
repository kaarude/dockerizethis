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

Flags mirror the Go CLI: `--dry-run`, `--yes`, `--json`,
`--verify none|build|full`, `--stack`, `--service`, `--force`, `--backup`.
See `npx dockerizethis --help` or the
[project README](https://github.com/kaarude/dockerizethis#readme).

## About the `dockerize` command name

The npm package name `dockerize` belongs to an unrelated, abandoned project
("Docker your Node apps", v0.1.0, unpublished since 2022). A bare
`npx dockerize` in a directory that does not already depend on this package
fetches *that* package instead — its `MODULE_NOT_FOUND` errors are not from
this project. To run the real `dockerize` bin name:

- **In a project** — install this package once, then the `dockerize` bin
  resolves locally and `npx dockerize` works:

  ```sh
  npm install --save-dev dockerizethis
  npx dockerize --dry-run
  ```

- **Ad hoc** — ask npx for this package but run its `dockerize` bin:

  ```sh
  npx -p dockerizethis dockerize ./my-app
  ```

- **Globally** — `npm install --global dockerizethis` puts `dockerize` on
  `PATH`, so plain `dockerize` and `npx dockerize` both resolve to it.

## Resolution order

1. `DOCKERIZE_BIN` environment variable, if set.
2. The `dockerizethis-<platform>-<arch>` package installed for this machine.
3. A `dockerize` binary found on `PATH` (e.g. from `go install`).

Unsupported platforms print a pointer to `go install` instead of failing
silently.
