# Node Dockerfile templates

`RenderNode(p plan.Plan) ([]emit.File, error)` renders embedded `text/template`
files without reading the project directory or modifying the plan. It returns
`Dockerfile` and `.dockerignore`, plus `nginx.conf` for a static site. Files have
mode `0644`. It does not emit `.env.example`.

The caller supplies a completed Node plan with a numeric version, package manager,
process, and a start command for web or worker processes. Web and static plans
also need a port. `Workdir` is not a host path to copy; the container uses `/app`.
Commands may use local executables from `/app/node_modules/.bin`.

| Extras key | Meaning |
| --- | --- |
| `lockfile` | Override the manager's default: `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, or `bun.lockb`. Also accepts `npm-shrinkwrap.json` for npm and `bun.lock` for Bun. |
| `nativeDeps` | A nonempty value requests Python, make, and g++ in the deps stage and prevents a distroless runtime. |
| `distroless` | The exact value `true` requests distroless for web or worker plans without native dependencies. |
| `staticDir` | Static output directory relative to `/app`, defaulting to `dist`. |

Distroless requires a major Node version and a direct `node` command. Quoted
arguments are supported; shell expressions and package-manager commands return
an error because the image has no shell or package manager. The Node entrypoint
comes from the base image. Healthchecks use its absolute Node executable path.

Bun and Yarn Classic installs use `--frozen-lockfile`; modern Yarn uses
`--immutable`. Mismatched manifests fail the build.

Build dependencies are installed even with `NODE_ENV=production`, so build tools
listed in `devDependencies` are available. Web and worker images retain the app
directory and installed dependencies. Compiler packages stay out of the runtime
stage. Yarn uses the node-modules linker so a direct Node command can load packages.
Custom workspace layouts and package-manager configuration files are not handled
by the manifest-and-lockfile dependency stage.

Static sites run nginx as UID 1001, with PID and temporary files under `/tmp`.
Missing paths return 404. With no build command, the output directory must already
exist in the build context.

Append renderer warnings when assembling the final plan:

```go
p.Notes = append(p.Notes, node.RenderNotes(p)...)
files, err := node.RenderNode(p)
```

Warnings also appear as Dockerfile comments. A web plan without `HealthPath`
produces a warning and no healthcheck instruction.

```sh
go build ./... && go test ./...
go test ./internal/templates/node -update
go test ./internal/templates/node -docker -run TestDocker -v -timeout 25m
```

The optional Docker test builds and runs small apps with all four package
managers, nginx, and distroless. It checks runtime identity, compiler isolation,
HTTP responses, and healthcheck success and failure without publishing host ports.
It needs a running Docker daemon and registry access, and removes its own images
and containers afterward.
