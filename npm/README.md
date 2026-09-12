# npm packaging (`npx dockerizethis`)

This directory packages the `dockerize` Go binary for npm so users can run it
with `npx` — no Go toolchain needed. The layout follows the pattern used by
esbuild and Biome: one thin JS package plus one package per platform holding
a prebuilt binary.

```
npm/
  dockerizethis/            the published root package (name: dockerizethis)
    bin/dockerize.js        shim: resolves the platform binary, spawns it
  scripts/
    build-npm-packages.sh   assembles + packs every npm package
  dist/                     generated staging dirs and .tgz files (ignored)
```

## How it works

The root package `dockerizethis` lists `dockerizethis-<os>-<cpu>` packages in
`optionalDependencies`. Each platform package carries `os`/`cpu` fields so npm
installs only the one matching the user's machine. At run time
`bin/dockerize.js` resolves that package's `bin/dockerize[.exe]`, then falls
back to `$DOCKERIZE_BIN` or a `dockerize` on `PATH`.

## Local smoke test (no publishing)

```sh
go build -o /tmp/dockerize ./cmd/dockerize
npm/scripts/build-npm-packages.sh 0.0.0-dev --local /tmp/dockerize
mkdir /tmp/npx-test && cd /tmp/npx-test
npm init -y && npm install "$OLDPWD/npm/dist/packed/"*.tgz
npx dockerize --help
```

## Release flow

After a tagged `goreleaser release` (or `goreleaser release --snapshot` for a
dry run):

```sh
npm/scripts/build-npm-packages.sh 1.2.3 dist/
npm publish npm/dist/packed/dockerizethis-*.tgz
```

Platform tarballs must be published alongside (or before) the root package —
`optionalDependencies` tolerate a missing package, but users on that platform
would hit the "no prebuilt binary" error.

Note: the name `dockerize` is already taken on npm, hence `dockerizethis`.
The shim still registers a `dockerize` bin, so `npx dockerize` works inside
projects that installed this package — only the *package* name differs.
