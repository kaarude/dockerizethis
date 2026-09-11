# Python artifacts

`RenderPython(plan.Plan)` emits Dockerfile and .dockerignore as mode 0644 files.
It accepts pip, poetry, uv, and pdm. The builder installs dependencies into
/opt/venv; the runtime copies that environment and the source, then runs as
UID 10001. Python versions must be numeric image tags. Plans are not mutated.

Pip installs requirements.txt by default. `Extras["pipSource"]` can select
pyproject.toml or setup.py to install the project instead.
`Extras["pythonpath"]="/app/src"` supports source-layout modules.
Postgres/MySQL services add their native build and runtime libraries.
Other native extensions may need additional system packages.

Poetry uses its [export plugin](https://python-poetry.org/docs/cli/#export),
uv uses [locked sync](https://docs.astral.sh/uv/guides/integration/docker/),
and PDM exports locked production requirements. Tool installation happens only
in the builder. StartCmd runs through `sh -c` with `exec`; pass a direct process
command. Web plans expose their port and get a Python HTTP healthcheck when
HealthPath is set. Workers get neither instruction.

Run unit/golden tests with `go test ./internal/templates/python`.
Refresh goldens with the per-package `-update` flag.
Run fixture images, healthchecks, native-driver imports, and all four package
manager builds with:

```sh
DOCKERIZETHIS_DOCKER_TEST=1 go test ./internal/templates/python -run TestDocker -v -timeout 20m
```

The Docker tests pull public images/packages and remove only the containers
and image tags they create. Discord runs offline without a token. Django uses
a test-only secret supplied at runtime.
