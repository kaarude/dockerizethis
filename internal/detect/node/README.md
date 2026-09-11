# Node detector

Use `node.Detector{}` through the frozen `detect.Detector` interface. Detection
reads project files without installing dependencies or executing scripts.

- Lockfile priority is pnpm, Yarn, Bun, then npm. Bun prefers `bun.lock`
  over `bun.lockb`; npm prefers `npm-shrinkwrap.json` over `package-lock.json`.
  Nondefault filenames are passed to the renderer in `Extras["lockfile"]`.
  No lockfile means npm with `Extras["lockfile"]="none"`, so the renderer
  installs without one, and a note recommends committing package-lock.json.
- Version priority is `engines.node`, `.nvmrc`, then `.node-version`, with
  `20` as the default. Exact numeric versions and common lower-bound ranges
  yield a numeric version. This is a heuristic, not a full semver solver.
  Unsupported aliases and upper-bound selectors fall through to the next source.
- Dependencies and dev dependencies both count. Framework priority follows
  Next, Nuxt, Express, Fastify, Koa, Hono, then Astro.
- A server dependency with a start script takes precedence over worker evidence.
  Worker dependencies take precedence over static build tooling. Astro alone
  counts as static tooling. A runtime command with a JavaScript or TypeScript
  entry point counts as worker evidence when no server dependency exists.
- `scripts.start` becomes `StartCmd`; `main` supplies a quoted `node` command
  when no start script exists. A build script yields `npm run build`.
  Unclear processes and missing start commands produce notes.
- Source scanning covers `.ts`, `.js`, `.mjs`, `.cjs`, `.tsx`, `.jsx`, and `.vue`.
  It skips dependency/build directories, tests, examples, fixtures, and source
  symlinks. Environment names are deduplicated and sorted. Database connection
  variables and common credential names are marked required; `PORT` and
  `NODE_ENV` are not.
- The existing `process.env.PORT` fallback scan supplies the port first.
  Next and Nuxt otherwise use 3000, Vite static projects use 4173, and other
  static projects use 8080. This environment scan is still a text heuristic.
- Literal listen ports and direct GET health routes are inferred only in the
  file named by a plain `node <file>` start command, or `main` when no start
  script exists. A single top-level `const` app must come from an Express,
  Fastify, or Koa import/require and an empty constructor call. Koa supplies
  listen ports only. Numeric expressions, conflicting ports, imported routers,
  wrapper servers, middleware, and nested calls are not resolved.
  Comments and strings cannot supply calls. Regex/division syntax, interpolated
  templates, or non-ASCII code outside literals disable this inference for the
  file. JSX/TSX startup files are skipped. An unresolved web port stays at 0,
  with a note; rendering still requires a valid port.
- Direct GET routes preserve `/health`, `/healthz`, and their trailing slashes.
  Conventional Next `app`/`pages` roots, including `src`, map to literal URL
  paths. Route groups are omitted; dynamic, private, parallel, and intercepted
  paths are skipped. App route handlers need an explicit GET export; pages
  need a default export. Nuxt recognizes GET or method-independent handlers
  under `server/api` and `server/routes`, plus Vue pages. Custom routing config,
  base paths, authentication, and handler responses are not evaluated. A
  detected health URL is a candidate that still needs runtime verification.
- A static project with an explicit `react-scripts build` command, or with
  react-scripts installed without Vite, records
  `Extras["staticDir"]="build"`, matching the `react-scripts build` output
  directory. Everything else defaults to `dist` at render time.
- A service requires both its environment name and matching driver dependency.
  Prisma or `@prisma/client` adds Postgres and the requested migration note.
  Services are deduplicated and sorted.

Confidence starts at 0.5. Each category adds 0.1 once: a lockfile, an explicit
version, recognized framework or worker/static dependency, a start command, a
build script, source environment references, and inferred services. The score
is capped at 1.0. It expresses evidence, not successful container verification.

Run `go build ./... && go test ./...` from the repository root. Fixture tests
compare the entire plan for Express/Postgres, a Discord worker, and Next.js.
