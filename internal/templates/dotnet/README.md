# .NET artifacts

`RenderDotnet(plan.Plan)` emits Dockerfile and .dockerignore as mode 0644
files. It validates the stack, numeric version, process, web port, package
manager, start command, and that Extras["projectFile"] is a project path
inside the build context. The caller's plan is not mutated.

The build runs `dotnet publish` on mcr.microsoft.com/dotnet/sdk:VERSION.
Web plans run on the aspnet image and workers on the runtime image, with the
published output in /app. .NET 8 and newer base images include the non-root
`app` user (uid 1654), which the Dockerfile selects; older versions run as
root.

Run unit/golden tests with `go test ./internal/templates/dotnet`.
Refresh goldens with the per-package `-update` flag.
Run actual image builds, web requests, and worker checks with:

```sh
DOCKERIZETHIS_DOCKER_TEST=1 go test ./internal/templates/dotnet -run TestDocker -v
```

The Docker tests pull public images and remove only the containers and image
tags they create.
