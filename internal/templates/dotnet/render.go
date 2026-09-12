// Package dotnet renders Docker artifacts for .NET projects.
package dotnet

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
)

//go:embed templates/*.tmpl
var templateFS embed.FS
var templates = template.Must(template.New("dotnet").ParseFS(templateFS, "templates/*.tmpl"))

var (
	versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
	projectPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*\.(?:cs|fs|vb)proj$`)
)

type renderData struct {
	plan.Plan
	ProjectFile     string
	TargetFramework string
	NonRoot         bool
	Command         string
}

// RenderDotnet returns a Dockerfile and .dockerignore without changing p.
// Extras["projectFile"] selects the project dotnet publish builds; it must
// be a .csproj/.fsproj/.vbproj path inside the build context. Web plans run
// on the aspnet image, workers on the runtime image. The non-root app user
// exists in .NET 8+ base images; older versions run as root with a note in
// DEPLOY.md owned by the caller.
func RenderDotnet(p plan.Plan) ([]emit.File, error) {
	if p.Stack != "dotnet" {
		return nil, fmt.Errorf("render dotnet: unsupported stack %q", p.Stack)
	}
	if !versionPattern.MatchString(p.Version) {
		return nil, fmt.Errorf("render dotnet: version must be a numeric .NET version")
	}
	switch p.Process {
	case plan.ProcessWeb:
		if p.Port < 1 || p.Port > 65535 {
			return nil, fmt.Errorf("render dotnet: port must be between 1 and 65535")
		}
	case plan.ProcessWorker:
	default:
		return nil, fmt.Errorf("render dotnet: unsupported process %q", p.Process)
	}
	if p.PkgManager != "dotnet" {
		return nil, fmt.Errorf("render dotnet: unsupported package manager %q", p.PkgManager)
	}
	if strings.TrimSpace(p.StartCmd) == "" || strings.ContainsAny(p.StartCmd, "\r\n\x00") {
		return nil, fmt.Errorf("render dotnet: start command must be a nonempty single line")
	}
	d := renderData{Plan: p, Command: jsonArray([]string{"/bin/sh", "-c", "exec " + p.StartCmd})}
	if project := p.Extras["projectFile"]; project != "" {
		if !projectPattern.MatchString(project) || path.Clean(project) != project || strings.HasPrefix(project, "..") {
			return nil, fmt.Errorf("render dotnet: projectFile must be a project path inside the build context")
		}
		d.ProjectFile = project
	}
	if tfm := p.Extras["targetFramework"]; tfm != "" {
		if tfm != "net"+p.Version {
			return nil, fmt.Errorf("render dotnet: targetFramework must match the runtime version")
		}
		d.TargetFramework = tfm
	}
	if major, _ := strconv.Atoi(strings.SplitN(p.Version, ".", 2)[0]); major >= 8 {
		d.NonRoot = true
	}
	var files []emit.File
	for _, artifact := range []struct{ path, template string }{{"Dockerfile", "Dockerfile.tmpl"}, {".dockerignore", "dockerignore.tmpl"}} {
		var content bytes.Buffer
		if err := templates.ExecuteTemplate(&content, artifact.template, d); err != nil {
			return nil, fmt.Errorf("render dotnet %s: %w", artifact.path, err)
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
