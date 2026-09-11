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
		{"rust-web", plan.Plan{Stack: "rust", Version: "1.85", PkgManager: "cargo", Framework: "axum", Process: plan.ProcessWeb, Port: 8080, HealthPath: "/health", Env: []plan.EnvVar{{Name: "PORT"}}, BuildCmd: "cargo build --release", StartCmd: "rust-web", Workdir: "/app", Confidence: 0.9, Extras: map[string]string{"bin": "rust-web"}}},
		{"rust-worker", plan.Plan{Stack: "rust", Version: "1", PkgManager: "cargo", Process: plan.ProcessWorker, Env: []plan.EnvVar{{Name: "DISCORD_TOKEN", Required: true}}, BuildCmd: "cargo build --release", StartCmd: "rust-worker", Workdir: "/app", Confidence: 0.6, Extras: map[string]string{"bin": "rust-worker"}, Notes: []string{"no rust-version or toolchain channel; using latest stable Rust"}}},
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
	require.Equal(t, "/health", p.HealthPath)
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
