package rust_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/carl/dockerizethis/internal/detect/rust"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/stretchr/testify/require"
)

func TestFixtures(t *testing.T) {
	for _, tc := range []struct {
		name string
		want plan.Plan
	}{
		{"rust-web", plan.Plan{Stack: "rust", Version: "1.85", PkgManager: "cargo", Framework: "axum", Process: plan.ProcessWeb, Port: 8080, HealthPath: "/health", Env: []plan.EnvVar{{Name: "PORT"}}, BuildCmd: "cargo build --release --bin rust-web", StartCmd: "rust-web", Workdir: "/app", Confidence: 0.9, Extras: map[string]string{"bin": "rust-web"}}},
		{"rust-worker", plan.Plan{Stack: "rust", Version: "1", PkgManager: "cargo", Process: plan.ProcessWorker, Env: []plan.EnvVar{{Name: "DISCORD_TOKEN", Required: true}}, BuildCmd: "cargo build --release --bin rust-worker", StartCmd: "rust-worker", Workdir: "/app", Confidence: 0.6, Extras: map[string]string{"bin": "rust-worker"}, Notes: []string{"no rust-version or toolchain channel; using latest stable Rust"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ok, err := (rust.Detector{}).Detect(filepath.Join("..", "..", "..", "testdata", "fixtures", tc.name))
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, p)
		})
	}
	require.Equal(t, "rust", (rust.Detector{}).Name())
}

func TestDependenciesServicesAndEnv(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Cargo.toml", `[package]
name = "api"
edition = "2021"
rust-version = "1.85"

[[bin]]
name = "api-server"
path = "src/main.rs"

[dependencies]
actix-web = "4"
sqlx = { version = "0.8", features = ["postgres", "runtime-tokio"] }
redis = "0.27"
mongodb = "3"
mysql_async = "0.34"
openssl = "0.10"
serde.workspace = true
# commented-dep = "1"
`)
	write(t, dir, "src/main.rs", `use actix_web::{get, App, HttpServer};

#[get("/health")]
async fn health() -> &'static str { "ok" }

#[actix_web::main]
async fn main() {
    let _db = std::env::var("DATABASE_URL").unwrap();
    let _token = std::env::var("API_TOKEN");
    HttpServer::new(|| App::new())
        .bind(("0.0.0.0", 9090)).unwrap()
        .run().await.unwrap();
}`)
	write(t, dir, "target/ignored.rs", `env::var("IGNORED")`)
	p, ok, err := (rust.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "actix-web", p.Framework)
	require.Equal(t, plan.ProcessWeb, p.Process)
	require.Equal(t, 9090, p.Port)
	require.Empty(t, p.HealthPath, "an attribute alone does not prove the handler is mounted")
	require.Equal(t, "1.85", p.Version)
	require.Equal(t, "api-server", p.StartCmd, "[[bin]] name overrides the package name")
	require.Equal(t, "api-server", p.Extras["bin"])
	require.Equal(t, "ssl", p.Extras["nativeDeps"])
	require.Equal(t, []plan.Service{plan.ServiceMongo, plan.ServiceMySQL, plan.ServicePostgres, plan.ServiceRedis}, p.Services)
	require.Equal(t, []plan.EnvVar{{Name: "API_TOKEN", Required: true}, {Name: "DATABASE_URL", Required: true}}, p.Env)
}

func TestToolchainAndPorts(t *testing.T) {
	for _, tc := range []struct {
		name, toolchain, file, source string
		version                       string
		port                          int
	}{
		{"toolchain file", "1.78\n", "rust-toolchain", "", "1.78", 3000},
		{"toolchain toml", "[toolchain]\nchannel = \"1.79.0-x86_64-unknown-linux-gnu\"\n", "rust-toolchain.toml", "", "1.79.0", 3000},
		{"named channel", "nightly\n", "rust-toolchain", "", "1", 3000},
		{"addr port", "", "", `fn main() { axum::serve(TcpListener::bind("0.0.0.0:3000").await.unwrap(), app); }`, "1", 3000},
		{"tuple port", "", "", `fn main() { Server::bind(&([0, 0, 0, 0], 4000).into()); }`, "1", 4000},
		{"config port", "", "", `fn main() { rocket::build().configure(rocket::Config { port: 9000, ..Default::default() }); }`, "1", 9000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "Cargo.toml", "[package]\nname = \"app\"\n\n[dependencies]\naxum = \"0.7\"\n")
			if tc.toolchain != "" {
				write(t, dir, tc.file, tc.toolchain)
			}
			write(t, dir, "src/main.rs", tc.source)
			p, ok, err := (rust.Detector{}).Detect(dir)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.version, p.Version)
			require.Equal(t, tc.port, p.Port)
		})
	}
}

func TestWorkspaceAndLibrary(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Cargo.toml", "[workspace]\nmembers = [\"crates/api\", \"crates/worker\"]\n")
	p, ok, err := (rust.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Less(t, p.Confidence, 0.6)
	require.Empty(t, p.StartCmd)
	require.Contains(t, p.Notes[len(p.Notes)-1], "workspace")
}

func TestAbsentAndFailures(t *testing.T) {
	dir := t.TempDir()
	p, ok, err := (rust.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, plan.Plan{}, p)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "Cargo.toml"), 0o755))
	_, ok, err = (rust.Detector{}).Detect(dir)
	require.ErrorContains(t, err, "read Cargo.toml")
	require.False(t, ok)
	dir = t.TempDir()
	write(t, dir, "Cargo.toml", "edition = \"2021\"\n")
	_, ok, err = (rust.Detector{}).Detect(dir)
	require.ErrorContains(t, err, "missing [package] or [workspace]")
	require.False(t, ok)
}

func TestWorkerPortNote(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Cargo.toml", "[package]\nname = \"bot\"\n")
	write(t, dir, "src/main.rs", `fn main() { let l = std::net::TcpListener::bind("0.0.0.0:5000").unwrap(); }`)
	p, ok, err := (rust.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, plan.ProcessWorker, p.Process)
	require.Zero(t, p.Port)
	require.Contains(t, p.Notes[len(p.Notes)-1], "worker")
}

func write(t *testing.T, dir, name, data string) {
	t.Helper()
	filename := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	require.NoError(t, os.WriteFile(filename, []byte(data), 0o644))
}

func TestBinarySelection(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		files          []string
		want           string
	}{
		{"sole bin", "", []string{"src/bin/worker.rs"}, "worker"},
		{"directory bin", "", []string{"src/bin/worker/main.rs"}, "worker"},
		{"empty bin directory", "", []string{"src/bin/worker/helper.rs"}, ""},
		{"multiple", "", []string{"src/bin/a.rs", "src/bin/b.rs"}, ""},
		{"default run", "default-run = \"b\"", []string{"src/bin/a.rs", "src/bin/b.rs"}, "b"},
		{"bad default run", "default-run = \"missing\"", []string{"src/main.rs"}, ""},
		{"autobins disabled", "autobins = false", []string{"src/main.rs"}, ""},
		{"library", "", []string{"src/lib.rs"}, ""},
		{"renamed main", "\n[[bin]]\nname = \"worker\"\npath = \"src/main.rs\"", []string{"src/main.rs"}, "worker"},
		{"explicit multiple", "\n[[bin]]\nname = \"a\"\n[[bin]]\nname = \"b\"", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "Cargo.toml", "[package]\nname = \"tools\"\n"+tc.manifest+"\n")
			for _, file := range tc.files {
				write(t, dir, file, "fn main() {}")
			}
			p, ok, err := (rust.Detector{}).Detect(dir)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, p.Extras["bin"])
			if tc.want == "" {
				require.Empty(t, p.Process)
				require.NotEmpty(t, p.Notes)
			} else {
				require.Equal(t, plan.ProcessWorker, p.Process)
				require.Contains(t, p.BuildCmd, "--bin "+tc.want)
			}
		})
	}
}

func TestDependencySubtables(t *testing.T) {
	var plans []plan.Plan
	for _, deps := range []string{
		"[dependencies]\naxum = \"0.8\"\nsqlx = { version = \"0.8\", features = [\"postgres\"] }\nopenssl = \"0.10\"\n",
		"[dependencies.axum]\nversion = \"0.8\"\n[dependencies.sqlx]\nversion = \"0.8\"\nfeatures = [\n\"postgres\",\n]\n[dependencies.openssl]\nversion = \"0.10\"\n",
		"[target.'cfg(unix)'.dependencies.axum]\nversion = \"0.8\"\n[target.'cfg(unix)'.dependencies.sqlx]\nfeatures = [\"postgres\"]\n[target.'cfg(unix)'.dependencies.openssl]\nversion = \"0.10\"\n",
	} {
		dir := t.TempDir()
		write(t, dir, "Cargo.toml", "[package]\nname = \"app\"\n"+deps)
		write(t, dir, "src/main.rs", "fn main() {}")
		p, ok, err := (rust.Detector{}).Detect(dir)
		require.NoError(t, err)
		require.True(t, ok)
		plans = append(plans, p)
	}
	require.Equal(t, plans[0], plans[1])
	require.Equal(t, plans[0], plans[2])
	require.Equal(t, "axum", plans[0].Framework)
	require.Equal(t, []plan.Service{plan.ServicePostgres}, plans[0].Services)
	require.Equal(t, "ssl", plans[0].Extras["nativeDeps"])
}

func TestHealthRoutes(t *testing.T) {
	for _, tc := range []struct{ name, code, file, want string }{
		{"exact GET", `let app = Router::new().route("/healthz", get(health));`, "src/main.rs", "/healthz"},
		{"POST", `let app = Router::new().route("/health", post(health));`, "src/main.rs", ""},
		{"comment", `// let app = Router::new().route("/health", get(health));`, "src/main.rs", ""},
		{"block comment", `/* let app = Router::new().route("/health", get(health)); */`, "src/main.rs", ""},
		{"string", `let example = r#"Router::new().route("/health", get(health))"#;`, "src/main.rs", ""},
		{"mounted", `let app = Router::new().route("/health", get(health)); Router::new().nest("/api", app);`, "src/main.rs", ""},
		{"test directory", `let app = Router::new().route("/health", get(health));`, "tests/health.rs", ""},
		{"inline tests", `#[cfg(test)] mod tests { let app = Router::new().route("/health", get(health)); }`, "src/main.rs", ""},
		{"test attribute", `#[test] fn test() { let app = Router::new().route("/health", get(health)); }`, "src/main.rs", ""},
		{"attribute", `#[get("/health")] fn health() {}`, "src/main.rs", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "Cargo.toml", "[package]\nname = \"app\"\n[dependencies]\naxum = \"0.8\"\n")
			write(t, dir, "src/main.rs", "fn main() {}")
			write(t, dir, tc.file, tc.code)
			p, ok, err := (rust.Detector{}).Detect(dir)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, p.HealthPath)
		})
	}
}
