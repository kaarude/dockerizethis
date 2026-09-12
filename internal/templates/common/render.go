// Package common renders stack-independent artifacts for a plan:
// docker-compose.yml, a GitHub Actions workflow that builds and pushes the
// image, and DEPLOY.md deploy notes.
package common

import (
	"bytes"
	"embed"
	"fmt"
	"slices"
	"text/template"

	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var templates = template.Must(template.New("common").Funcs(template.FuncMap{
	// gh quotes a GitHub Actions expression: gh "github.ref" -> ${{ github.ref }}.
	"gh": func(expr string) string { return "${{ " + expr + " }}" },
}).ParseFS(templateFS, "templates/*.tmpl"))

// serviceVolumes maps each known backing service to its named volume, or ""
// when the service stores nothing worth persisting. Services outside this set
// get a TODO comment instead of a guessed compose block.
var serviceVolumes = map[plan.Service]string{
	plan.ServicePostgres: "postgres_data",
	plan.ServiceRedis:    "",
	plan.ServiceMySQL:    "mysql_data",
	plan.ServiceMongo:    "mongo_data",
}

type commonData struct {
	plan.Plan
	Web            bool
	Worker         bool
	Static         bool
	UnknownProcess bool
	RequiredEnv    bool
	DependsOn      []plan.Service
	UnknownSvcs    []plan.Service
	Volumes        []string
}

func prepare(p plan.Plan) commonData {
	d := commonData{Plan: p, RequiredEnv: slices.ContainsFunc(p.Env, func(v plan.EnvVar) bool { return v.Required })}
	switch p.Process {
	case plan.ProcessWeb:
		d.Web = true
	case plan.ProcessWorker:
		d.Worker = true
	case plan.ProcessStatic:
		d.Static = true
	default:
		d.UnknownProcess = true
	}
	seen := map[plan.Service]bool{}
	for _, s := range p.Services {
		if seen[s] {
			continue
		}
		seen[s] = true
		volume, known := serviceVolumes[s]
		if !known {
			d.UnknownSvcs = append(d.UnknownSvcs, s)
			continue
		}
		d.DependsOn = append(d.DependsOn, s)
		if volume != "" {
			d.Volumes = append(d.Volumes, volume)
		}
	}
	return d
}

func render(name, path string, p plan.Plan) (emit.File, error) {
	var content bytes.Buffer
	if err := templates.ExecuteTemplate(&content, name, prepare(p)); err != nil {
		return emit.File{}, fmt.Errorf("render common %s: %w", path, err)
	}
	return emit.File{Path: path, Content: content.Bytes(), Mode: 0o644}, nil
}

// RenderCompose renders docker-compose.yml: an app service built from the
// generated Dockerfile plus a block for each detected backing service. Every
// known service carries a healthcheck, so app can wait on
// condition: service_healthy. It never emits a .env file; compose resolves
// ${VAR} references from the project's own .env, and env_file is listed only
// when the plan declares variables. The file is optional if none are required,
// so the app can use its defaults without a manually created .env.
func RenderCompose(p plan.Plan) (emit.File, error) {
	if (p.Process == plan.ProcessWeb || p.Process == plan.ProcessStatic) && (p.Port < 1 || p.Port > 65535) {
		return emit.File{}, fmt.Errorf("render compose: web/static port must be between 1 and 65535")
	}
	return render("compose.yml.tmpl", "docker-compose.yml", p)
}

// RenderAction renders .github/workflows/docker.yml for the target repo: a
// Buildx workflow that pushes ghcr.io/<repo> on main and version tags using
// GITHUB_TOKEN.
func RenderAction(p plan.Plan) (emit.File, error) {
	return render("docker.yml.tmpl", ".github/workflows/docker.yml", p)
}

// RenderDeployDoc renders DEPLOY.md: a VPS quickstart plus per-process and
// per-service notes. Plan values dockerize does not know become TODO
// comments rather than wrong instructions.
func RenderDeployDoc(p plan.Plan) (emit.File, error) {
	return render("DEPLOY.md.tmpl", "DEPLOY.md", p)
}
