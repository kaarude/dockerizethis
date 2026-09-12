// Package rust renders Docker artifacts for Cargo projects.
package rust

import (
	"bytes"
	"embed"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
)

//go:embed templates/*.tmpl
var templateFS embed.FS
var templates = template.Must(template.New("rust").ParseFS(templateFS, "templates/*.tmpl"))

var (
	versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,2}$`)
	binPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// nativeDeps maps a detector token to the apt packages the builder and the
// Debian runtime need for crates that link system libraries.
var nativeDeps = map[string]struct{ build, runtime string }{
	"ssl":         {"pkg-config libssl-dev", "libssl3"},
	"pq":          {"libpq-dev", "libpq5"},
	"mysqlclient": {"pkg-config default-libmysqlclient-dev", "libmariadb3"},
}

type renderData struct {
	plan.Plan
	Bin         string
	BinPackage  string
	BuildPkgs   string
	RuntimePkgs string
}

// RenderRust returns a Dockerfile and .dockerignore without changing p.
// Extras["bin"] names the binary copied to /app/server, defaulting to
// StartCmd. Extras["binPackage"] adds cargo --package for workspaces.
// Extras["nativeDeps"] lists space-separated groups: ssl, pq, mysqlclient.
func RenderRust(p plan.Plan) ([]emit.File, error) {
	if p.Stack != "rust" {
		return nil, fmt.Errorf("render rust: unsupported stack %q", p.Stack)
	}
	if !versionPattern.MatchString(p.Version) {
		return nil, fmt.Errorf("render rust: version must be a numeric Rust version")
	}
	switch p.Process {
	case plan.ProcessWeb:
		if p.Port < 1 || p.Port > 65535 {
			return nil, fmt.Errorf("render rust: port must be between 1 and 65535")
		}
	case plan.ProcessWorker:
	default:
		return nil, fmt.Errorf("render rust: unsupported process %q", p.Process)
	}
	bin := p.Extras["bin"]
	if bin == "" {
		bin = strings.TrimSpace(p.StartCmd)
	}
	if !binPattern.MatchString(bin) {
		return nil, fmt.Errorf("render rust: cannot determine the binary to build; set Extras[\"binPackage\"] and Extras[\"bin\"]")
	}
	d := renderData{Plan: p, Bin: bin, BinPackage: p.Extras["binPackage"]}
	if d.BinPackage != "" && !binPattern.MatchString(d.BinPackage) {
		return nil, fmt.Errorf("render rust: binPackage must be a Cargo package name")
	}
	var build, runtime []string
	for _, group := range strings.Fields(p.Extras["nativeDeps"]) {
		pkgs, ok := nativeDeps[group]
		if !ok {
			return nil, fmt.Errorf("render rust: unsupported nativeDeps group %q", group)
		}
		build = append(build, pkgs.build)
		runtime = append(runtime, pkgs.runtime)
	}
	slices.Sort(build)
	slices.Sort(runtime)
	d.BuildPkgs, d.RuntimePkgs = strings.Join(build, " "), strings.Join(runtime, " ")
	var files []emit.File
	for _, artifact := range []struct{ path, template string }{{"Dockerfile", "Dockerfile.tmpl"}, {".dockerignore", "dockerignore.tmpl"}} {
		var content bytes.Buffer
		if err := templates.ExecuteTemplate(&content, artifact.template, d); err != nil {
			return nil, fmt.Errorf("render rust %s: %w", artifact.path, err)
		}
		files = append(files, emit.File{Path: artifact.path, Content: content.Bytes(), Mode: 0o644})
	}
	return files, nil
}
