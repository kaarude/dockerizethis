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
`bin/dockerize.js` uses `$DOCKERIZE_BIN` when set, otherwise resolves that
package's `bin/dockerize[.exe]`, then falls back to a `dockerize` on `PATH`.

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
set -e
version=1.2.3
npm/scripts/build-npm-packages.sh "$version" --goreleaser dist/
for package in ./npm/dist/packed/dockerizethis-*.tgz; do
  [ "$package" = "./npm/dist/packed/dockerizethis-$version.tgz" ] && continue
  npm publish "$package"
done
npm publish "./npm/dist/packed/dockerizethis-$version.tgz"
```

Set `version` to the release version. Publish platform
tarballs before the root package. Missing optional dependencies do not fail
installation, but users on those platforms cannot run the bundled binary.
To check the publish commands without uploading anything, add `--dry-run` to
both `npm publish` commands. For prereleases, also pass `--tag next` to both.

## The `dockerize` name collision

The npm name `dockerize` belongs to an unrelated abandoned package
(v0.1.0, "Docker your Node apps"), so a bare `npx dockerize` with no local
install fetches *that* — its crashes are not ours. Workable invocations,
all covered in `dockerizethis/README.md`:

- `npx dockerizethis` — the public entrypoint.
- `npx -p dockerizethis dockerize` — runs this package's `dockerize` bin.
- `npm i -D dockerizethis` in a project → `npx dockerize` resolves the
  local `.bin/dockerize` shim before npx ever asks the registry.
- `npm i -g dockerizethis` → `dockerize` on `PATH`; npx prefers it over
  the squatted package.

If the `dockerize` name is ever transferred or a scope is preferred
(`npx @scope/dockerize`), only `dockerizethis/package.json`'s `name` and
the platform-package map in `bin/dockerize.js` change.
