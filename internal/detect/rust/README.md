# Rust detection

`rust.Detector{}` implements `detect.Detector` and reports stack `rust`.
It scans Cargo.toml and `.rs` sources with a line scanner and regular
expressions. It does not parse the full TOML grammar, evaluate cfg
expressions, or execute the project. target/, vendor/, .git, test files are
not special-cased beyond the skipped directories.

The plan includes the package or `[[bin]]` name, the Rust toolchain version,
the `cargo build --release` command, web frameworks (axum, actix-web, rocket,
warp, poem, tide, salvo, hyper), backing services from dependency names and
features (sqlx, diesel, sea-orm, redis, mongodb, mysql_async), and literal
`env::var`/`var_os` names. Ports come from literal bind addresses, tuple
socket constructors, `.port(...)` calls, or a `PORT` env fallback; framework
defaults are axum 3000, rocket 8000, otherwise 8080.

The version is `[package] rust-version`, then a numeric channel from
rust-toolchain.toml or rust-toolchain, then latest stable `1` with a note.
Named channels such as stable or nightly fall through to the default because
they are not usable as image tags here.

A `[workspace]` manifest without `[package]` produces a low-confidence plan
that needs `Extras["binPackage"]` (cargo `-p` member) and `Extras["bin"]`
(binary copied to /app/server) before rendering. `Extras["nativeDeps"]`
lists space-separated groups (`ssl`, `pq`, `mysqlclient`) when crates that
link native libraries are declared.
