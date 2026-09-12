# Java artifacts

`RenderJava(plan.Plan)` emits Dockerfile and .dockerignore as mode 0644
files. It validates the stack, numeric version, process, web port, package
manager, and that the build and start commands are single lines. The
caller's plan is not mutated.

Maven builds run on maven:3.9-eclipse-temurin-VERSION and copy
`target/*.jar`; Gradle builds run on gradle:jdkVERSION and copy
`build/libs/*.jar`. Quarkus copies the complete target/quarkus-app or
build/quarkus-app directory into /app/quarkus-app, including its libraries, and
runs quarkus-run.jar in place. Other plans copy their jar to /app/app.jar.
The runtime is eclipse-temurin:VERSION-jre with a non-root user. Multiple matching
jars fail the copy and require an unambiguous build artifact.

Run unit/golden tests with `go test ./internal/templates/java`.
Refresh goldens with the per-package `-update` flag.
Run actual image builds and web requests with:

```sh
DOCKERIZETHIS_DOCKER_TEST=1 go test ./internal/templates/java -run TestDocker -v
```

The Docker tests pull public images and remove only the containers and image
tags they create.
