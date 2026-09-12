# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through [GitHub security
advisories](https://github.com/kaarude/dockerizethis/security/advisories/new)
("Report a vulnerability" on the repository's Security tab). Do not put exploit
details or secrets in a public issue.

Include the affected commit or release, reproduction steps, and the impact you
observed.

## What dockerize does with secrets

dockerize reads a project to detect its stack and writes Docker artifacts into that
project. It does not copy secret values anywhere.

The `.env.example` it generates lists variable names, whether each one is required, and a
hint that describes the value. It never reads values out of your real `.env`, your shell
environment, or your secret manager, and it never writes a value into a generated file.
If you find a generated artifact that contains a live secret, treat it as a security bug
and report it using the process above.

## Scope

The CLI runs locally under your user account. It reads files in the project you point it
at and writes artifacts next to them. Verification may run `docker build` and start a
container, which executes your project's build and start commands. Run it against
projects you trust.

The following are out of scope: vulnerabilities in Docker itself, in a project's own
dependencies, or in artifacts you edited after generating them.

## Supported versions

Security fixes land on the latest tagged release and on `main`. Older releases are not
maintained.
