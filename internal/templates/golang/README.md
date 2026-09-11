# Go artifacts

`RenderGo(plan.Plan)` emits Dockerfile and .dockerignore as mode 0644 files.
It validates the stack, version, process, web port, and build-package path.
The caller's plan is not mutated. Runtime workdir and binary are always
/app and /app/server.

The default build disables CGO and uses a distroless non-root runtime.
`Extras["cgo"]="1"` enables CGO, installs the standard C build tools, and selects
Alpine with CA certificates and C/C++ runtime libraries. Projects needing other
native libraries need additional image customization.
`Extras["buildPackage"]` defaults to `.` and can select `./cmd/service`.
Go module downloads are cached before source is copied, with or without go.sum.

Run unit/golden tests with `go test ./internal/templates/golang`.
Refresh goldens with the per-package `-update` flag.
Run actual image builds, web requests, and worker checks with:

```sh
DOCKERIZETHIS_DOCKER_TEST=1 go test ./internal/templates/golang -run TestDocker -v
```

The Docker tests pull public images and remove only the containers and image
tags they create.
