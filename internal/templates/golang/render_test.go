package golang_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	detector "github.com/carl/dockerizethis/internal/detect/golang"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/templates/golang"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update Go template golden files")

func webPlan() plan.Plan {
	return plan.Plan{Stack: "go", Version: "1.23", PkgManager: "go", Process: plan.ProcessWeb, Port: 8080, StartCmd: "service", Workdir: "/app", Extras: map[string]string{}}
}

func TestRenderGoGolden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*plan.Plan)
	}{
		{"web", func(p *plan.Plan) {}},
		{"worker", func(p *plan.Plan) { p.Process, p.Port = plan.ProcessWorker, 0 }},
		{"cgo", func(p *plan.Plan) { p.Extras["cgo"] = "1" }},
		{"cmd-layout", func(p *plan.Plan) { p.Extras["buildPackage"] = "./cmd/service" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := golang.RenderGo(p)
			require.NoError(t, err)
			var output bytes.Buffer
			for _, file := range files {
				fmt.Fprintf(&output, "--- %s (mode %04o) ---\n%s", file.Path, file.Mode, file.Content)
			}
			golden := filepath.Join("..", "..", "..", "testdata", "golden", "go-"+tc.name+".golden")
			if *update {
				require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
				require.NoError(t, os.WriteFile(golden, output.Bytes(), 0o644))
			}
			want, err := os.ReadFile(golden)
			require.NoError(t, err)
			require.Equal(t, string(want), output.String(), "refresh with go test ./internal/templates/golang -update")
		})
	}
}

func TestFixtureArtifacts(t *testing.T) {
	for _, fixture := range []string{"go-http", "go-worker"} {
		t.Run(fixture, func(t *testing.T) {
			p, ok, err := (detector.Detector{}).Detect(filepath.Join("..", "..", "..", "testdata", "fixtures", fixture))
			require.NoError(t, err)
			require.True(t, ok)
			files, err := golang.RenderGo(p)
			require.NoError(t, err)
			require.Len(t, files, 2)
			require.Equal(t, "Dockerfile", files[0].Path)
			require.Equal(t, ".dockerignore", files[1].Path)
			dockerfile := string(files[0].Content)
			require.Less(t, strings.Index(dockerfile, "RUN go mod download"), strings.Index(dockerfile, "COPY . ."))
			require.Contains(t, dockerfile, "USER 65532:65532\n")
			require.Contains(t, dockerfile, `CMD ["/app/server"]`)
			require.Equal(t, p.Process == plan.ProcessWeb, strings.Contains(dockerfile, "EXPOSE "))
			for _, file := range files {
				require.EqualValues(t, 0o644, file.Mode)
			}
		})
	}
}

func TestRenderGoInvalidPlan(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*plan.Plan)
	}{
		{"stack", "stack", func(p *plan.Plan) { p.Stack = "node" }},
		{"version", "version", func(p *plan.Plan) { p.Version = "1.23\nRUN bad" }},
		{"missing version", "version", func(p *plan.Plan) { p.Version = "" }},
		{"process", "process", func(p *plan.Plan) { p.Process = plan.ProcessStatic }},
		{"port", "port", func(p *plan.Plan) { p.Port = 0 }},
		{"large port", "port", func(p *plan.Plan) { p.Port = 65536 }},
		{"cgo", "cgo", func(p *plan.Plan) { p.Extras["cgo"] = "yes" }},
		{"parent package", "buildPackage", func(p *plan.Plan) { p.Extras["buildPackage"] = "./.." }},
		{"package traversal", "buildPackage", func(p *plan.Plan) { p.Extras["buildPackage"] = "./../outside" }},
		{"package injection", "buildPackage", func(p *plan.Plan) { p.Extras["buildPackage"] = ".; echo bad" }},
		{"absolute package", "buildPackage", func(p *plan.Plan) { p.Extras["buildPackage"] = "/tmp/main" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := golang.RenderGo(p)
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, files)
		})
	}
}
