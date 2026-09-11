# Dockerizethis

Dockerizethis turns a detected project into Docker artifacts that the project owner can edit and verify.

## Language

**Detection plan**:
A description inferred from project files, including how the application builds, starts, and connects to backing services.

**Verification**:
A check that the project's Docker image builds and, when requested for a web application, answers a startup probe. Verification includes removing the temporary container used for the probe.

**Startup probe**:
An HTTP request that checks whether a built web application starts. A detected health endpoint must return a successful or redirect response; without one, a non-server-error response proves HTTP startup only.
_Avoid_: Deployment health certification
