# Rust detection

`rust.Detector{}` reads Cargo.toml and Rust sources without running project code.
It supports dependency entries and dependency subtables, including feature arrays
and target-specific declarations. It does not evaluate cfg expressions or resolve
workspace inheritance. Build and development dependencies do not imply a server.

Binary selection follows `src/main.rs`, `src/bin/name.rs`, `src/bin/name/main.rs`,
and explicit `[[bin]]` names and paths. `autobins = false` disables discovery.
Multiple binaries require `package.default-run`. Library-only packages and virtual
workspace roots leave the process undetermined with a note.

The plan records the selected binary in `Extras["bin"]` and builds it with
`cargo build --release --bin NAME`. Web dependencies identify axum, actix-web,
rocket, warp, poem, tide, salvo, and hyper. Driver names and dependency features
identify backing services and native library groups in `Extras["nativeDeps"]`.

The Rust version comes from `package.rust-version`, a numeric rust-toolchain
channel, or `1`. Named channels fall back to `1` with a note. Port inference uses
literal addresses, socket tuples, framework configuration, and PORT fallbacks.
Framework defaults are axum 3000, rocket 8000, and otherwise 8080.

Health inference preserves literal `/health...` paths on direct Axum
`Router::new().route(PATH, get(...))` declarations. Comments, strings, test paths,
examples, and benchmarks do not provide health evidence. Nesting, merging, or cfg
attributes disable health inference. Attribute routes and other framework routing
forms remain unspecified because their mounting cannot be established statically.
