// Package python renders Docker artifacts for Python applications.
package python

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
)

//go:embed templates/*.tmpl
var templateFS embed.FS
var templates = template.Must(template.New("python").ParseFS(templateFS, "templates/*.tmpl"))
var versionPattern = regexp.MustCompile(`^3\.[0-9]+(?:\.[0-9]+)?$`)

type renderData struct {
	plan.Plan
	Postgres, MySQL      bool
	PipProject           bool
	Command, Healthcheck string
}

// RenderPython returns a Dockerfile and .dockerignore without changing p.
// Extras["pipSource"] selects requirements.txt (default), pyproject.toml, or setup.py.
// Extras["pythonpath"]="/app/src" supports source-layout entry modules.
func RenderPython(p plan.Plan) ([]emit.File, error) {
	if p.Stack != "python" {
		return nil, fmt.Errorf("render python: unsupported stack %q", p.Stack)
	}
	if !versionPattern.MatchString(p.Version) {
		return nil, fmt.Errorf("render python: version must be a numeric Python version")
	}
	switch p.Process {
	case plan.ProcessWeb:
		if p.Port < 1 || p.Port > 65535 {
			return nil, fmt.Errorf("render python: port must be between 1 and 65535")
		}
	case plan.ProcessWorker:
	default:
		return nil, fmt.Errorf("render python: unsupported process %q", p.Process)
	}
	switch p.PkgManager {
	case "pip", "poetry", "uv", "pdm":
	default:
		return nil, fmt.Errorf("render python: unsupported package manager %q", p.PkgManager)
	}
	if strings.TrimSpace(p.StartCmd) == "" || strings.ContainsAny(p.StartCmd, "\r\n\x00") {
		return nil, fmt.Errorf("render python: start command must be a nonempty single line")
	}
	d := renderData{Plan: p, Postgres: slices.Contains(p.Services, plan.ServicePostgres), MySQL: slices.Contains(p.Services, plan.ServiceMySQL), Command: jsonArray([]string{"/bin/sh", "-c", "exec " + p.StartCmd})}
	switch p.Extras["pipSource"] {
	case "", "requirements.txt":
	case "pyproject.toml", "setup.py":
		d.PipProject = true
	default:
		return nil, fmt.Errorf("render python: unsupported pipSource")
	}
	if value := p.Extras["pythonpath"]; value != "" && value != "/app/src" {
		return nil, fmt.Errorf("render python: pythonpath must be /app/src")
	}
	if p.Process == plan.ProcessWeb && p.HealthPath != "" {
		if strings.ContainsAny(p.HealthPath, "\r\n\x00") || !strings.HasPrefix(p.HealthPath, "/") || strings.HasPrefix(p.HealthPath, "//") {
			return nil, fmt.Errorf("render python: health path must be a local absolute URL path")
		}
		if _, err := url.ParseRequestURI(p.HealthPath); err != nil {
			return nil, fmt.Errorf("render python: invalid health path: %w", err)
		}
		// JSON strings are Python literals except for escapes such as \/, which this encoder never emits.
		address, _ := json.Marshal(fmt.Sprintf("http://127.0.0.1:%d%s", p.Port, p.HealthPath))
		d.Healthcheck = jsonArray([]string{"python", "-c", "import urllib.request; urllib.request.urlopen(" + string(address) + ", timeout=3).close()"})
	}
	var files []emit.File
	for _, artifact := range []struct{ path, template string }{{"Dockerfile", "Dockerfile.tmpl"}, {".dockerignore", "dockerignore.tmpl"}} {
		var content bytes.Buffer
		if err := templates.ExecuteTemplate(&content, artifact.template, d); err != nil {
			return nil, fmt.Errorf("render python %s: %w", artifact.path, err)
		}
		files = append(files, emit.File{Path: artifact.path, Content: content.Bytes(), Mode: 0o644})
	}
	return files, nil
}

func jsonArray(values []string) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(values)
	return strings.TrimSuffix(buf.String(), "\n")
}
