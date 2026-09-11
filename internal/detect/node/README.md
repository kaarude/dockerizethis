# Node detector

Use `node.Detector{}` through the frozen `detect.Detector` interface. Detection
reads project files without installing dependencies or executing scripts.

- Lockfile priority is pnpm, Yarn, Bun, then npm. Bun prefers `bun.lock`
  over `bun.lockb`; npm prefers `npm-shrinkwrap.json` over `package-lock.json`.
  Nondefault filenames are passed to the renderer in `Extras["lockfile"]`.
  No lockfile means npm.
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
- Source scanning covers `.ts`, `.js`, and `.mjs`. It skips `node_modules`,
  `dist`, `build`, and `.git` directories at any depth, plus source symlinks.
  Environment names are deduplicated and sorted. Database connection variables
  and common credential names are marked required; `PORT` and `NODE_ENV` are not.
- A numeric fallback beside `process.env.PORT` supplies the port. Otherwise
  Next and Nuxt use 3000, Vite static projects use 4173, and other projects use 0.
  With multiple fallbacks, the first valid one in lexical file order wins.
  The scan does not evaluate JavaScript or resolve arbitrary variable assignments.
- A service requires both its environment name and matching driver dependency.
  Prisma or `@prisma/client` adds Postgres and the requested migration note.
  Services are deduplicated and sorted.

Confidence starts at 0.5. Each category adds 0.1 once: a lockfile, an explicit
version, recognized framework or worker/static dependency, a start command, a
build script, source environment references, and inferred services. The score
is capped at 1.0. It expresses evidence, not successful container verification.

Run `go build ./... && go test ./...` from the repository root. Fixture tests
compare the entire plan for Express/Postgres, a Discord worker, and Next.js.
