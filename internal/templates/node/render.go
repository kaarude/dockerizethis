// Package node renders Docker build artifacts for Node projects.
package node

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"text/template"

	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var (
	templates      = template.Must(template.New("node").ParseFS(templateFS, "templates/*.tmpl"))
	versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,2}$`)
	pathPattern    = regexp.MustCompile(`^[a-zA-Z0-9_.-]+(/[a-zA-Z0-9_.-]+)*$`)
)

type renderData struct {
	plan.Plan
	Lockfile    string
	InstallCmd  string
	NativeDeps  bool
	Distroless  bool
	StaticDir   string
	Command     string
	Healthcheck string
	Warnings    []string
}

// RenderNode returns a Dockerfile, .dockerignore, and, for static sites, nginx.conf.
// Extras["lockfile"] overrides the package manager's default lockfile name;
// "none" means no lockfile exists, so npm installs without one.
// Extras["staticDir"] selects the static output directory, defaulting to dist.
// Distroless requires a major Node version and a direct node command;
// shell expressions and package-manager start scripts need the Alpine runtime.
// Native dependencies always select Alpine, even when distroless is requested.
func RenderNode(p plan.Plan) ([]emit.File, error) {
	d, err := prepare(p)
	if err != nil {
		return nil, fmt.Errorf("render node: %w", err)
	}
	artifacts := []struct{ path, template string }{
		{"Dockerfile", string(p.Process) + ".Dockerfile.tmpl"},
		{".dockerignore", "dockerignore.tmpl"},
	}
	if p.Process == plan.ProcessStatic {
		artifacts = append(artifacts, struct{ path, template string }{"nginx.conf", "nginx.conf.tmpl"})
	}
	files := make([]emit.File, 0, len(artifacts))
	for _, artifact := range artifacts {
		var content bytes.Buffer
		if err := templates.ExecuteTemplate(&content, artifact.template, d); err != nil {
			return nil, fmt.Errorf("render node %s: %w", artifact.path, err)
		}
		files = append(files, emit.File{Path: artifact.path, Content: content.Bytes(), Mode: 0o644})
	}
	return files, nil
}

// RenderNotes returns rendering warnings for the caller to append to p.Notes.
// RenderNode takes its plan by value and never mutates it.
func RenderNotes(p plan.Plan) []string {
	var notes []string
	if p.Process == plan.ProcessWeb && p.HealthPath == "" {
		notes = append(notes, "No healthcheck generated: set HealthPath to an HTTP endpoint.")
	}
	if p.Process != plan.ProcessStatic && p.Extras["distroless"] == "true" && strings.TrimSpace(p.Extras["nativeDeps"]) != "" {
		notes = append(notes, "Using Alpine instead of distroless because native dependencies were detected.")
	}
	return notes
}

func prepare(p plan.Plan) (renderData, error) {
	d := renderData{Plan: p, NativeDeps: strings.TrimSpace(p.Extras["nativeDeps"]) != "", Warnings: RenderNotes(p)}
	if p.Stack != "node" {
		return d, fmt.Errorf("unsupported stack %q", p.Stack)
	}
	if !versionPattern.MatchString(p.Version) {
		return d, fmt.Errorf("version must be a numeric Node version")
	}
	switch p.Process {
	case plan.ProcessWeb, plan.ProcessStatic:
		if p.Port < 1 || p.Port > 65535 {
			return d, fmt.Errorf("port must be between 1 and 65535")
		}
	case plan.ProcessWorker:
	default:
		return d, fmt.Errorf("unsupported process %q", p.Process)
	}
	switch p.PkgManager {
	case "npm":
		d.Lockfile, d.InstallCmd = "package-lock.json", "npm ci"
	case "pnpm":
		d.Lockfile, d.InstallCmd = "pnpm-lock.yaml", "pnpm i --frozen-lockfile"
	case "yarn":
		d.Lockfile, d.InstallCmd = "yarn.lock", `sh -c 'case "$(yarn --version)" in 1.*) yarn --frozen-lockfile ;; *) yarn --immutable ;; esac'`
	case "bun":
		d.Lockfile, d.InstallCmd = "bun.lockb", "bun i --frozen-lockfile"
	default:
		return d, fmt.Errorf("unsupported package manager %q", p.PkgManager)
	}
	if lockfile := p.Extras["lockfile"]; lockfile != "" {
		// Installers discover their own lockfile, so arbitrary filenames won't work.
		supported := lockfile == d.Lockfile ||
			(p.PkgManager == "npm" && lockfile == "npm-shrinkwrap.json") ||
			(p.PkgManager == "bun" && lockfile == "bun.lock")
		switch {
		case lockfile == "none" && p.PkgManager == "npm":
			// No lockfile exists: nothing to COPY and npm ci cannot run.
			d.Lockfile, d.InstallCmd = "", "npm install"
		case supported:
			d.Lockfile = lockfile
		default:
			return d, fmt.Errorf("lockfile %q is not supported by %s", lockfile, p.PkgManager)
		}
	}
	if p.BuildCmd != "" && (!singleLine(p.BuildCmd) || strings.TrimSpace(p.BuildCmd) == "" || strings.HasSuffix(strings.TrimSpace(p.BuildCmd), "\\")) {
		return d, fmt.Errorf("build command must be a nonempty single line without a trailing backslash")
	}
	if p.Process == plan.ProcessStatic {
		d.StaticDir = p.Extras["staticDir"]
		if d.StaticDir == "" {
			d.StaticDir = "dist"
		}
		if !pathPattern.MatchString(d.StaticDir) || path.Clean(d.StaticDir) != d.StaticDir || d.StaticDir == "." || d.StaticDir == ".." || strings.HasPrefix(d.StaticDir, "../") {
			return d, fmt.Errorf("staticDir must be a relative output directory within /app")
		}
		return d, nil
	}
	if strings.TrimSpace(p.StartCmd) == "" || !singleLine(p.StartCmd) {
		return d, fmt.Errorf("start command must be a nonempty single line")
	}
	d.Distroless = p.Extras["distroless"] == "true" && !d.NativeDeps
	if d.Distroless {
		if strings.Contains(p.Version, ".") {
			return d, fmt.Errorf("distroless requires a major Node version")
		}
		args, err := nodeCommand(p.StartCmd)
		if err != nil {
			return d, err
		}
		// The distroless image already uses Node as its entrypoint.
		d.Command = jsonArray(args)
	} else {
		d.Command = jsonArray([]string{"/bin/sh", "-c", p.StartCmd})
	}
	if p.Process == plan.ProcessWeb && p.HealthPath != "" {
		if !singleLine(p.HealthPath) || !strings.HasPrefix(p.HealthPath, "/") || strings.HasPrefix(p.HealthPath, "//") {
			return d, fmt.Errorf("health path must be a local absolute URL path")
		}
		if _, err := url.ParseRequestURI(p.HealthPath); err != nil {
			return d, fmt.Errorf("invalid health path: %w", err)
		}
		address, _ := json.Marshal(fmt.Sprintf("http://127.0.0.1:%d%s", p.Port, p.HealthPath))
		script := "fetch(" + string(address) + ").then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"
		node := "node"
		if d.Distroless {
			node = "/nodejs/bin/node"
		}
		d.Healthcheck = jsonArray([]string{node, "-e", script})
	}
	return d, nil
}

func singleLine(value string) bool {
	return !strings.ContainsAny(value, "\r\n\x00")
}

func jsonArray(values []string) string {
	// A string slice is always JSON-marshalable.
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(values)
	return strings.TrimSuffix(buf.String(), "\n")
}

// nodeCommand parses literal shell words; shell evaluation needs Alpine.
func nodeCommand(command string) ([]string, error) {
	invalid := fmt.Errorf("distroless requires a direct node command, such as node dist/server.js; use Alpine for shell or package-manager commands")
	var args []string
	var word strings.Builder
	var quote rune
	started, escaped := false, false
	for _, c := range command {
		if escaped {
			// Inside double quotes the shell only consumes these escapes.
			if quote == '"' && !strings.ContainsRune("$`\"\\", c) {
				word.WriteRune('\\')
			}
			word.WriteRune(c)
			escaped = false
			continue
		}
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		if c == '\\' {
			escaped, started = true, true
			continue
		}
		if quote == '"' {
			if c == '$' || c == '`' {
				return nil, invalid
			}
			if c == '"' {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote, started = c, true
		case ' ', '\t':
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		case '$', '`', '&', '|', ';', '<', '>', '(', ')', '*', '?', '[', ']', '{', '}', '~', '#':
			return nil, invalid
		default:
			word.WriteRune(c)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, invalid
	}
	if started {
		args = append(args, word.String())
	}
	if len(args) < 2 || args[0] != "node" || args[1] == "" {
		return nil, invalid
	}
	return args[1:], nil
}
