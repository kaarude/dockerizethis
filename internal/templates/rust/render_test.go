package rust_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	detector "github.com/carl/dockerizethis/internal/detect/rust"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/templates/rust"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update Rust template golden files")

func webPlan() plan.Plan {
	return plan.Plan{Stack: "rust", Version: "1.85", PkgManager: "cargo", Process: plan.ProcessWeb, Port: 3000, BuildCmd: "cargo build --release", StartCmd: "api", Workdir: "/app", Extras: map[string]string{"bin": "api"}}
}

func TestRenderRustGolden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*plan.Plan)
	}{
		{"web", func(p *plan.Plan) {}},
		{"worker", func(p *plan.Plan) { p.Process, p.Port = plan.ProcessWorker, 0 }},
		{"workspace", func(p *plan.Plan) { p.Extras["binPackage"] = "api" }},
		{"native-deps", func(p *plan.Plan) { p.Extras["nativeDeps"] = "ssl pq" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := rust.RenderRust(p)
			require.NoError(t, err)
			var output bytes.Buffer
			for _, file := range files {
				fmt.Fprintf(&output, "--- %s (mode %04o) ---\n%s", file.Path, file.Mode, file.Content)
			}
			golden := filepath.Join("..", "..", "..", "testdata", "golden", "rust-"+tc.name+".golden")
			if *update {
				require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
				require.NoError(t, os.WriteFile(golden, output.Bytes(), 0o644))
			}
			want, err := os.ReadFile(golden)
			require.NoError(t, err)
			require.Equal(t, string(want), output.String(), "refresh with go test ./internal/templates/rust -update")
		})
	}
}

func TestFixtureArtifacts(t *testing.T) {
	for _, fixture := range []string{"rust-web", "rust-worker"} {
		t.Run(fixture, func(t *testing.T) {
			p, ok, err := (detector.Detector{}).Detect(filepath.Join("..", "..", "..", "testdata", "fixtures", fixture))
			require.NoError(t, err)
			require.True(t, ok)
			files, err := rust.RenderRust(p)
			require.NoError(t, err)
			require.Len(t, files, 2)
			require.Equal(t, "Dockerfile", files[0].Path)
			require.Equal(t, ".dockerignore", files[1].Path)
			dockerfile := string(files[0].Content)
			require.Contains(t, dockerfile, "RUN cargo build --release --bin "+p.Extras["bin"]+"\n")
			require.Contains(t, dockerfile, "USER 10001:10001\n")
			require.Contains(t, dockerfile, `CMD ["/app/server"]`)
			require.Equal(t, p.Process == plan.ProcessWeb, strings.Contains(dockerfile, "EXPOSE "))
			require.Contains(t, dockerfile, "target/release/"+p.Extras["bin"])
			for _, file := range files {
				require.EqualValues(t, 0o644, file.Mode)
			}
		})
	}
}

func TestRenderRustInvalidPlan(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*plan.Plan)
	}{
		{"stack", "stack", func(p *plan.Plan) { p.Stack = "go" }},
		{"version", "version", func(p *plan.Plan) { p.Version = "stable\nRUN bad" }},
		{"channel version", "version", func(p *plan.Plan) { p.Version = "nightly" }},
		{"missing version", "version", func(p *plan.Plan) { p.Version = "" }},
		{"process", "process", func(p *plan.Plan) { p.Process = plan.ProcessStatic }},
		{"port", "port", func(p *plan.Plan) { p.Port = 0 }},
		{"large port", "port", func(p *plan.Plan) { p.Port = 65536 }},
		{"missing bin", "binary", func(p *plan.Plan) { p.Extras["bin"] = ""; p.StartCmd = "" }},
		{"bad bin", "binary", func(p *plan.Plan) { p.Extras["bin"] = "../evil" }},
		{"bin injection", "binary", func(p *plan.Plan) { p.Extras["bin"] = "app; rm -rf /" }},
		{"bad package", "binPackage", func(p *plan.Plan) { p.Extras["binPackage"] = "pkg; rm -rf /" }},
		{"bad nativeDeps", "nativeDeps", func(p *plan.Plan) { p.Extras["nativeDeps"] = "rootkit" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := rust.RenderRust(p)
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, files)
		})
	}
}
