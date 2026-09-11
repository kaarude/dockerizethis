package python_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	detector "github.com/carl/dockerizethis/internal/detect/python"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/templates/python"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update Python template golden files")

func webPlan() plan.Plan {
	return plan.Plan{Stack: "python", Version: "3.12", PkgManager: "pip", Process: plan.ProcessWeb, Port: 8000, HealthPath: "/health", StartCmd: "uvicorn main:app --host 0.0.0.0 --port 8000", Workdir: "/app", Extras: map[string]string{}}
}

func TestRenderPythonGolden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*plan.Plan)
	}{
		{"web-pip", func(p *plan.Plan) {}},
		{"worker-pip", func(p *plan.Plan) { p.Process, p.Port, p.StartCmd = plan.ProcessWorker, 0, "python bot.py" }},
		{"web-poetry", func(p *plan.Plan) { p.PkgManager = "poetry" }},
		{"web-uv", func(p *plan.Plan) { p.PkgManager = "uv"; p.Extras["pythonpath"] = "/app/src" }},
		{"worker-pdm", func(p *plan.Plan) {
			p.Process, p.PkgManager, p.Port, p.StartCmd = plan.ProcessWorker, "pdm", 0, "python -m worker"
		}},
		{"pip-project", func(p *plan.Plan) { p.Extras["pipSource"] = "pyproject.toml" }},
		{"database-drivers", func(p *plan.Plan) { p.Services = []plan.Service{plan.ServicePostgres, plan.ServiceMySQL} }},
		{"pip-setup", func(p *plan.Plan) { p.Extras["pipSource"] = "setup.py" }},
		{"web-no-health", func(p *plan.Plan) { p.HealthPath = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := python.RenderPython(p)
			require.NoError(t, err)
			var output bytes.Buffer
			for _, file := range files {
				fmt.Fprintf(&output, "--- %s (mode %04o) ---\n%s", file.Path, file.Mode, file.Content)
			}
			golden := filepath.Join("..", "..", "..", "testdata", "golden", "py-"+tc.name+".golden")
			if *update {
				require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
				require.NoError(t, os.WriteFile(golden, output.Bytes(), 0o644))
			}
			want, err := os.ReadFile(golden)
			require.NoError(t, err)
			require.Equal(t, string(want), output.String(), "refresh with go test ./internal/templates/python -update")
		})
	}
}

func TestFixtureArtifacts(t *testing.T) {
	for _, fixture := range []string{"py-fastapi-pg", "py-django", "py-discord-worker"} {
		t.Run(fixture, func(t *testing.T) {
			p, ok, err := (detector.Detector{}).Detect(filepath.Join("..", "..", "..", "testdata", "fixtures", fixture))
			require.NoError(t, err)
			require.True(t, ok)
			before, err := json.Marshal(p)
			require.NoError(t, err)
			files, err := python.RenderPython(p)
			require.NoError(t, err)
			require.Len(t, files, 2)
			after, err := json.Marshal(p)
			require.NoError(t, err)
			require.Equal(t, before, after)
			dockerfile := string(files[0].Content)
			require.Contains(t, dockerfile, "USER 10001:10001\n")
			require.Contains(t, dockerfile, "COPY --from=builder /opt/venv /opt/venv\n")
			require.Equal(t, p.Process == plan.ProcessWeb, strings.Contains(dockerfile, "EXPOSE "))
			require.Equal(t, p.Process == plan.ProcessWeb && p.HealthPath != "", strings.Contains(dockerfile, "HEALTHCHECK "))
			for _, pattern := range []string{".venv", "__pycache__", "*.pyc", ".git", ".env*", "tests"} {
				require.Contains(t, strings.Split(string(files[1].Content), "\n"), pattern)
			}
			for _, file := range files {
				require.EqualValues(t, 0o644, file.Mode)
			}
		})
	}
}

func TestHealthcheckEscaping(t *testing.T) {
	p := webPlan()
	p.HealthPath = `/health?value="quoted"&other=$HOME`
	p.StartCmd = `python worker.py --name="two words"`
	files, err := python.RenderPython(p)
	require.NoError(t, err)
	for _, line := range strings.Split(string(files[0].Content), "\n") {
		if strings.HasPrefix(line, "HEALTHCHECK ") {
			var args []string
			require.NoError(t, json.Unmarshal([]byte(strings.SplitN(line, " CMD ", 2)[1]), &args))
			require.Equal(t, []string{"python", "-c"}, args[:2])
			require.Contains(t, args[2], "import urllib.request;")
			addressJSON := strings.SplitN(strings.SplitN(args[2], "urlopen(", 2)[1], ", timeout", 2)[0]
			var address string
			require.NoError(t, json.Unmarshal([]byte(addressJSON), &address))
			require.Equal(t, "http://127.0.0.1:8000"+p.HealthPath, address)
		}
		if strings.HasPrefix(line, "CMD ") {
			var args []string
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "CMD ")), &args))
			require.Equal(t, []string{"/bin/sh", "-c", "exec " + p.StartCmd}, args)
		}
	}
}

func TestRenderPythonInvalidPlan(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*plan.Plan)
	}{
		{"stack", "stack", func(p *plan.Plan) { p.Stack = "node" }},
		{"version", "version", func(p *plan.Plan) { p.Version = "3.12\nRUN bad" }},
		{"missing version", "version", func(p *plan.Plan) { p.Version = "" }},
		{"process", "process", func(p *plan.Plan) { p.Process = plan.ProcessStatic }},
		{"port", "port", func(p *plan.Plan) { p.Port = 0 }},
		{"large port", "port", func(p *plan.Plan) { p.Port = 65536 }},
		{"manager", "package manager", func(p *plan.Plan) { p.PkgManager = "other" }},
		{"start", "start command", func(p *plan.Plan) { p.StartCmd = " " }},
		{"start newline", "start command", func(p *plan.Plan) { p.StartCmd = "python x\nUSER root" }},
		{"source", "pipSource", func(p *plan.Plan) { p.Extras["pipSource"] = "../requirements.txt" }},
		{"pythonpath", "pythonpath", func(p *plan.Plan) { p.Extras["pythonpath"] = "/tmp\nUSER root" }},
		{"health authority", "health path", func(p *plan.Plan) { p.HealthPath = "//example.com/health" }},
		{"health external", "health path", func(p *plan.Plan) { p.HealthPath = "https://example.com/health" }},
		{"health malformed", "health path", func(p *plan.Plan) { p.HealthPath = "/%zz" }},
		{"health newline", "health path", func(p *plan.Plan) { p.HealthPath = "/health\n" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := python.RenderPython(p)
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, files)
		})
	}
}
