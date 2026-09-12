package dotnet_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	detector "github.com/carl/dockerizethis/internal/detect/dotnet"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/templates/dotnet"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update .NET template golden files")

func webPlan() plan.Plan {
	return plan.Plan{Stack: "dotnet", Version: "8.0", PkgManager: "dotnet", Framework: "aspnet", Process: plan.ProcessWeb, Port: 8080, BuildCmd: "dotnet publish app.csproj -c Release -o /app/out", StartCmd: "dotnet /app/app.dll", Workdir: "/app", Extras: map[string]string{"projectFile": "app.csproj"}}
}

func TestRenderDotnetGolden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*plan.Plan)
	}{
		{"web", func(p *plan.Plan) {}},
		{"worker", func(p *plan.Plan) {
			p.Process, p.Port, p.Framework = plan.ProcessWorker, 0, ""
			p.BuildCmd, p.StartCmd = "dotnet publish worker.csproj -c Release -o /app/out", "dotnet /app/worker.dll"
			p.Extras["projectFile"] = "worker.csproj"
		}},
		{"old-runtime", func(p *plan.Plan) { p.Version = "6.0" }},
		{"nested-project", func(p *plan.Plan) {
			p.Extras["projectFile"] = "src/Api/Api.csproj"
			p.BuildCmd = "dotnet publish src/Api/Api.csproj -c Release -o /app/out"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := dotnet.RenderDotnet(p)
			require.NoError(t, err)
			var output bytes.Buffer
			for _, file := range files {
				fmt.Fprintf(&output, "--- %s (mode %04o) ---\n%s", file.Path, file.Mode, file.Content)
			}
			golden := filepath.Join("..", "..", "..", "testdata", "golden", "dotnet-"+tc.name+".golden")
			if *update {
				require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
				require.NoError(t, os.WriteFile(golden, output.Bytes(), 0o644))
			}
			want, err := os.ReadFile(golden)
			require.NoError(t, err)
			require.Equal(t, string(want), output.String(), "refresh with go test ./internal/templates/dotnet -update")
		})
	}
}

func TestFixtureArtifacts(t *testing.T) {
	for _, fixture := range []string{"dotnet-web", "dotnet-worker"} {
		t.Run(fixture, func(t *testing.T) {
			p, ok, err := (detector.Detector{}).Detect(filepath.Join("..", "..", "..", "testdata", "fixtures", fixture))
			require.NoError(t, err)
			require.True(t, ok)
			files, err := dotnet.RenderDotnet(p)
			require.NoError(t, err)
			require.Len(t, files, 2)
			require.Equal(t, "Dockerfile", files[0].Path)
			require.Equal(t, ".dockerignore", files[1].Path)
			dockerfile := string(files[0].Content)
			require.Contains(t, dockerfile, "RUN dotnet publish "+p.Extras["projectFile"])
			require.Contains(t, dockerfile, "USER app\n")
			require.Contains(t, dockerfile, "exec "+p.StartCmd)
			require.Equal(t, p.Process == plan.ProcessWeb, strings.Contains(dockerfile, "EXPOSE "))
			for _, file := range files {
				require.EqualValues(t, 0o644, file.Mode)
			}
		})
	}
}

func TestRenderDotnetInvalidPlan(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*plan.Plan)
	}{
		{"stack", "stack", func(p *plan.Plan) { p.Stack = "go" }},
		{"version", "version", func(p *plan.Plan) { p.Version = "8.0\nRUN bad" }},
		{"missing version", "version", func(p *plan.Plan) { p.Version = "" }},
		{"process", "process", func(p *plan.Plan) { p.Process = plan.ProcessStatic }},
		{"port", "port", func(p *plan.Plan) { p.Port = 0 }},
		{"pkg manager", "package manager", func(p *plan.Plan) { p.PkgManager = "nuget" }},
		{"empty start", "start command", func(p *plan.Plan) { p.StartCmd = "" }},
		{"project traversal", "projectFile", func(p *plan.Plan) { p.Extras["projectFile"] = "../App.csproj" }},
		{"project extension", "projectFile", func(p *plan.Plan) { p.Extras["projectFile"] = "app.txt" }},
		{"project injection", "projectFile", func(p *plan.Plan) { p.Extras["projectFile"] = "app.csproj; rm -rf /" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := dotnet.RenderDotnet(p)
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, files)
		})
	}
}

func TestSelectedTargetFramework(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.csproj"), []byte(`<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFrameworks>net8.0;net9.0</TargetFrameworks></PropertyGroup></Project>`), 0644))
	p, ok, err := (detector.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "8.0", p.Version)
	require.Contains(t, p.BuildCmd, "--framework net8.0")
	files, err := dotnet.RenderDotnet(p)
	require.NoError(t, err)
	require.Contains(t, string(files[0].Content), "--framework net8.0\n")
	p.Extras["targetFramework"] = "net9.0"
	_, err = dotnet.RenderDotnet(p)
	require.ErrorContains(t, err, "targetFramework")
}
