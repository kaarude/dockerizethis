# Common artifact templates

Stack-independent renderers over embedded `text/template` files. Each takes a
completed `plan.Plan` by value, returns one `emit.File` with mode `0644`, and
never reads the project directory or mutates the plan.

- `RenderCompose(p)` renders `docker-compose.yml`: an `app` service with
  `build: .` and `restart: unless-stopped`, `env_file: .env` only when the
  plan declares variables, and a published port only for `web` plans. If every
  detected variable is optional, `env_file` uses `required: false` so missing
  `.env` files do not prevent startup. This needs Docker Compose 2.24.0 or
  newer. Plans with required settings keep a required `.env` reference. Each
  known backing service gets an image, a healthcheck, and
  `condition: service_healthy` in the app's `depends_on`. Postgres, MySQL, and
  Mongo get named volumes; Redis stays ephemeral.
- `RenderAction(p)` renders `.github/workflows/docker.yml` for the target
  repo: Buildx plus build-push, pushing `ghcr.io/<repo>` on `main` and
  `v*.*.*` tags with `GITHUB_TOKEN` auth.
- `RenderDeployDoc(p)` renders `DEPLOY.md`: a VPS quickstart, per-process
  notes, and per-service data and backup pointers.

A web plan with a port outside 1–65535 fails instead of producing a bad port
mapping. Unknown process types and unknown services produce `TODO` comments
rather than wrong instructions. Nothing here writes a `.env` file — the
Compose file loads an existing `.env` without changing its values.

```sh
go test ./internal/templates/common
go test ./internal/templates/common -update
```
