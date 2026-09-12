# Rust artifacts

`RenderRust(plan.Plan)` emits Dockerfile and .dockerignore as mode 0644
files. It validates the stack, numeric version, process, web port, binary
name, and workspace package. The caller's plan is not mutated.

The build is `cargo build --release --bin NAME` on rust:VERSION-bookworm; the runtime
is debian:bookworm-slim with a non-root user, CA certificates, and the
binary at /app/server. `Extras["bin"]` names the binary (default StartCmd);
`Extras["binPackage"]` adds `--package` for workspace members.
`Extras["nativeDeps"]` accepts `ssl`, `pq`, and `mysqlclient` and installs
the matching build and runtime apt packages. There is no lockfile install
step: Cargo.lock must be committed for reproducible builds, which is the
usual convention for binaries.

Run unit/golden tests with `go test ./internal/templates/rust`.
Refresh goldens with the per-package `-update` flag.
Run actual image builds, web requests, and worker checks with:

```sh
DOCKERIZETHIS_DOCKER_TEST=1 go test ./internal/templates/rust -run TestDocker -v
```

The Docker tests pull public images and remove only the containers and image
tags they create.
