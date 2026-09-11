// Package golang renders Docker artifacts for Go applications.
package golang

import (
	"bytes"
	"embed"
	"fmt"
	"path"
	"regexp"
	"strings"
	"text/template"

	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
)

//go:embed templates/*.tmpl
var templateFS embed.FS
var templates = template.Must(template.New("go").ParseFS(templateFS, "templates/*.tmpl"))
var versionPattern = regexp.MustCompile(`^1\.[0-9]+(?:\.[0-9]+)?$`)
var packagePattern = regexp.MustCompile(`^\./[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`)

type renderData struct {
	plan.Plan
	CGO          bool
	BuildPackage string
}

// RenderGo returns a Dockerfile and .dockerignore without changing p.
// Extras["cgo"]="1" enables CGO and selects Alpine for the runtime.
// Extras["buildPackage"] selects a relative main package, defaulting to ".".
func RenderGo(p plan.Plan) ([]emit.File, error) {
	if p.Stack != "go" {
		return nil, fmt.Errorf("render go: unsupported stack %q", p.Stack)
	}
	if !versionPattern.MatchString(p.Version) {
		return nil, fmt.Errorf("render go: version must be a numeric Go version")
	}
	switch p.Process {
	case plan.ProcessWeb:
		if p.Port < 1 || p.Port > 65535 {
			return nil, fmt.Errorf("render go: port must be between 1 and 65535")
		}
	case plan.ProcessWorker:
	default:
		return nil, fmt.Errorf("render go: unsupported process %q", p.Process)
	}
	target := p.Extras["buildPackage"]
	if target == "" {
		target = "."
	}
	if target != "." && (!packagePattern.MatchString(target) || "./"+path.Clean(target) != target || (path.Clean(target) == ".." || strings.HasPrefix(path.Clean(target), "../"))) {
		return nil, fmt.Errorf("render go: buildPackage must be a relative package within /app")
	}
	if p.Extras["cgo"] != "" && p.Extras["cgo"] != "0" && p.Extras["cgo"] != "1" {
		return nil, fmt.Errorf("render go: cgo must be 0 or 1")
	}
	d := renderData{Plan: p, CGO: p.Extras["cgo"] == "1", BuildPackage: target}
	var files []emit.File
	for _, artifact := range []struct{ path, template string }{{"Dockerfile", "Dockerfile.tmpl"}, {".dockerignore", "dockerignore.tmpl"}} {
		var content bytes.Buffer
		if err := templates.ExecuteTemplate(&content, artifact.template, d); err != nil {
			return nil, fmt.Errorf("render go %s: %w", artifact.path, err)
		}
		files = append(files, emit.File{Path: artifact.path, Content: content.Bytes(), Mode: 0o644})
	}
	return files, nil
}
