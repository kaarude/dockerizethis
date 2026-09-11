// Package python detects Python applications using source and manifest evidence.
package python

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/carl/dockerizethis/internal/detect"
	"github.com/carl/dockerizethis/internal/plan"
)

type Detector struct{}

var _ detect.Detector = Detector{}

func (Detector) Name() string { return "python" }

var (
	versionPattern    = regexp.MustCompile(`^(?:>=\s*|~=\s*|==\s*)?(3\.[0-9]+(?:\.[0-9]+)?)(?:$|[,\s])`)
	requiresPattern   = regexp.MustCompile(`(?m)^\s*requires-python\s*=\s*["']([^"']+)["']`)
	envPattern        = regexp.MustCompile(`\bos\s*\.\s*(?:getenv\s*\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|environ\s*\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\]|environ\s*\.\s*get\s*\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["'])`)
	importPattern     = regexp.MustCompile(`(?m)^\s*(?:from\s+([A-Za-z_][\w.]*)\s+import\b|import\s+([^\n;]+))`)
	dependencyPattern = regexp.MustCompile(`(?m)(?:^\s*|["'])([A-Za-z][A-Za-z0-9_.-]*)(?:\[|[<>=!~; @"']|\s*$)`)
	modulePattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	appPattern        = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?::[^=\n]+)?=\s*(?:[A-Za-z_][A-Za-z0-9_]*\.)?(FastAPI|Flask|Starlette)\s*\(`)
	healthPattern     = regexp.MustCompile(`(?:\.(?:get|route|add_url_rule)\s*\(\s*["']/|\b(?:re_)?path\s*\(\s*["']/?)(healthz?)/?["']`)
)

func (Detector) Detect(dir string) (plan.Plan, bool, error) {
	manifests := map[string]string{}
	for _, name := range []string{"pyproject.toml", "requirements.txt", "setup.py"} {
		data, err := readOptional(dir, name)
		if err != nil {
			return plan.Plan{}, false, err
		}
		if data != nil {
			manifests[name] = string(data)
		}
	}
	if len(manifests) == 0 {
		return plan.Plan{}, false, nil
	}
	p := plan.Plan{Stack: "python", Version: "3.12", PkgManager: "pip", Process: plan.ProcessWorker, Workdir: "/app", Confidence: 0.9}
	for _, manager := range []string{"poetry", "uv", "pdm"} {
		data, err := readOptional(dir, manager+".lock")
		if err != nil {
			return plan.Plan{}, false, err
		}
		if data != nil {
			p.PkgManager = manager
			break
		}
	}
	if p.PkgManager != "pip" {
		if _, ok := manifests["pyproject.toml"]; !ok {
			return plan.Plan{}, false, fmt.Errorf("%s requires pyproject.toml", p.PkgManager)
		}
	}
	data, err := readOptional(dir, ".python-version")
	if err != nil {
		return plan.Plan{}, false, err
	}
	version := versionPattern.FindStringSubmatch(strings.TrimSpace(string(data)))
	if version == nil {
		if requirement := requiresPattern.FindStringSubmatch(manifests["pyproject.toml"]); requirement != nil {
			version = versionPattern.FindStringSubmatch(requirement[1])
		}
	}
	if version != nil {
		p.Version = version[1]
	}
	if p.PkgManager == "pip" {
		if _, ok := manifests["requirements.txt"]; !ok {
			source := "setup.py"
			if _, ok := manifests["pyproject.toml"]; ok {
				source = "pyproject.toml"
			}
			p.Extras = map[string]string{"pipSource": source}
		}
	}
	deps, names, sources := map[string]bool{}, map[string]bool{}, map[string]string{}
	for name, manifest := range manifests {
		for _, match := range dependencyPattern.FindAllStringSubmatch(dependencyText(name, manifest), -1) {
			deps[normalize(match[1])] = true
		}
	}
	err = filepath.WalkDir(dir, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if filename != dir {
				switch entry.Name() {
				case ".venv", "venv", "env", "tests", "test", ".git", "__pycache__", "site-packages", "node_modules":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !entry.Type().IsRegular() || filepath.Ext(filename) != ".py" || filepath.Base(filename) == "setup.py" || strings.HasPrefix(entry.Name(), "test_") || strings.HasSuffix(entry.Name(), "_test.py") {
			return nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, filename)
		if err != nil {
			return err
		}
		source := stripComments(string(data))
		sources[filepath.ToSlash(rel)] = source
		for _, match := range envPattern.FindAllStringSubmatch(source, -1) {
			for _, name := range match[1:] {
				if name != "" {
					names[name] = true
					break
				}
			}
		}
		for _, match := range importPattern.FindAllStringSubmatch(source, -1) {
			if match[1] != "" {
				deps[normalize(strings.Split(match[1], ".")[0])] = true
			}
			for _, item := range strings.Split(match[2], ",") {
				fields := strings.Fields(item)
				if len(fields) > 0 {
					deps[normalize(strings.Split(fields[0], ".")[0])] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return plan.Plan{}, false, fmt.Errorf("scan Python sources: %w", err)
	}
	for _, framework := range []string{"fastapi", "flask", "django", "starlette", "discord.py"} {
		if deps[framework] || (framework == "discord.py" && deps["discord"]) {
			p.Framework = framework
			break
		}
	}
	web := slices.Contains([]string{"fastapi", "flask", "django", "starlette"}, p.Framework)
	worker := deps["celery"] || deps["dramatiq"] || p.Framework == "discord.py"
	if web || (!worker && (deps["uvicorn"] || deps["gunicorn"])) {
		p.Process, p.Port = plan.ProcessWeb, 8000
	}
	for name := range deps {
		if strings.HasPrefix(name, "psycopg") {
			deps["psycopg"] = true
			break
		}
	}
	for _, service := range []struct {
		name plan.Service
		deps []string
	}{
		{plan.ServicePostgres, []string{"psycopg", "psycopg2", "psycopg2-binary", "psycopg-binary", "asyncpg", "sqlalchemy"}},
		{plan.ServiceRedis, []string{"redis"}}, {plan.ServiceMongo, []string{"pymongo"}}, {plan.ServiceMySQL, []string{"mysqlclient", "mysqldb"}},
	} {
		for _, name := range service.deps {
			if deps[name] {
				p.Services = append(p.Services, service.name)
				break
			}
		}
	}
	slices.Sort(p.Services)
	for name := range names {
		p.Env = append(p.Env, plan.EnvVar{Name: name, Required: requiredEnv(name)})
	}
	slices.SortFunc(p.Env, func(a, b plan.EnvVar) int { return strings.Compare(a.Name, b.Name) })
	entry, object, guessed := entryPoint(p, sources)
	if strings.HasPrefix(entry, "src/") {
		if p.Extras == nil {
			p.Extras = map[string]string{}
		}
		p.Extras["pythonpath"] = "/app/src"
	}
	module := moduleName(entry)
	switch {
	case p.Process == plan.ProcessWeb && (p.Framework == "django" || p.Framework == "flask"):
		p.StartCmd = "gunicorn " + module + ":" + object + " --bind 0.0.0.0:8000"
		if !deps["gunicorn"] {
			p.Notes = append(p.Notes, "gunicorn is required by the start command; add it to the project dependencies")
		}
	case p.Process == plan.ProcessWeb:
		p.StartCmd = "uvicorn " + module + ":" + object + " --host 0.0.0.0 --port 8000"
		if !deps["uvicorn"] {
			p.Notes = append(p.Notes, "uvicorn is required by the start command; add it to the project dependencies")
		}
	case strings.HasSuffix(entry, "/__main__.py"):
		p.StartCmd = "python -m " + strings.TrimSuffix(module, ".__main__")
	default:
		p.StartCmd = "python " + entry
	}
	if guessed {
		p.Confidence = 0.5
		p.Notes = append(p.Notes, "entry point guessed as "+entry+"; confirm the start command")
	}
	if p.Process == plan.ProcessWeb {
		for _, name := range slices.Sorted(maps.Keys(sources)) {
			if match := healthPattern.FindStringSubmatch(sources[name]); match != nil {
				p.HealthPath = "/" + match[1]
				break
			}
		}
	}
	return p, true, nil
}

// Prefer conventional entries; accept other modules only with application evidence.
func entryPoint(p plan.Plan, sources map[string]string) (string, string, bool) {
	var files []string
	for name := range sources {
		if modulePattern.MatchString(moduleName(name)) {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	preferred := []string{"main.py", "app.py", "server.py", "src/main.py", "src/app.py", "src/server.py", "worker.py", "bot.py"}
	files = append(preferred, files...)
	if p.Framework == "django" {
		for _, name := range files {
			if strings.HasSuffix(name, "/wsgi.py") || name == "wsgi.py" {
				if strings.Contains(sources[name], "get_wsgi_application(") {
					return name, "application", false
				}
			}
		}
		return "config/wsgi.py", "application", true
	}
	if p.Process == plan.ProcessWeb {
		constructor := map[string]string{"fastapi": "FastAPI", "flask": "Flask", "starlette": "Starlette"}[p.Framework]
		for _, name := range files {
			for _, match := range appPattern.FindAllStringSubmatch(sources[name], -1) {
				if constructor == "" || match[2] == constructor {
					return name, match[1], false
				}
			}
		}
		for _, name := range files {
			if _, ok := sources[name]; ok && !strings.HasSuffix(name, "__init__.py") {
				return name, "app", true
			}
		}
		return "main.py", "app", true
	}
	for _, name := range files {
		if strings.HasSuffix(name, "/__main__.py") {
			return name, "", false
		}
	}
	for _, name := range files {
		if source, ok := sources[name]; ok && !strings.HasSuffix(name, "__init__.py") && name != "manage.py" {
			// A script name alone doesn't prove it starts a long-running process.
			return name, "", !strings.Contains(source, `if __name__ == "__main__":`) && !strings.Contains(source, `if __name__ == '__main__':`)
		}
	}
	return "main.py", "", true
}

// Read dependency declarations, not project names, descriptions, or dev tool settings.
func dependencyText(filename, source string) string {
	source = stripComments(source)
	if filename == "requirements.txt" {
		return source
	}
	if filename == "setup.py" {
		match := regexp.MustCompile(`(?s)\binstall_requires\s*=\s*(\[(?:[^]"']|"[^"]*"|'[^']*')*\])`).FindStringSubmatch(source)
		if match != nil {
			return match[1]
		}
		return ""
	}
	var result, project strings.Builder
	section := ""
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = trimmed
			continue
		}
		if section == "[tool.poetry.dependencies]" {
			result.WriteString(line)
			result.WriteByte('\n')
		}
		if section == "[project]" {
			project.WriteString(line)
			project.WriteByte('\n')
		}
	}
	// Keep brackets inside dependency extras, such as psycopg[binary], inside their strings.
	pattern := regexp.MustCompile(`(?ms)^\s*dependencies\s*=\s*\[(?:[^]"']|"[^"]*"|'[^']*')*\]`)
	result.WriteString(pattern.FindString(project.String()))
	return result.String()
}

func moduleName(filename string) string {
	return strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(filename, "src/"), ".py"), "/", ".")
}
func normalize(name string) string { return strings.ReplaceAll(strings.ToLower(name), "_", "-") }
func readOptional(dir, name string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return data, nil
}

// Remove comments and multiline strings while preserving ordinary string literals.
// This is a lexical scan, not a Python or TOML interpreter.
func stripComments(source string) string {
	var out strings.Builder
	for i := 0; i < len(source); {
		if source[i] == '#' {
			for i < len(source) && source[i] != '\n' {
				i++
			}
			continue
		}
		if source[i] == '\'' || source[i] == '"' {
			quote := source[i]
			start := i
			i++
			if i+1 < len(source) && source[i] == quote && source[i+1] == quote {
				i += 2
				end := strings.Index(source[i:], strings.Repeat(string(quote), 3))
				if end < 0 {
					out.WriteString(strings.Repeat("\n", strings.Count(source[start:], "\n")))
					break
				}
				out.WriteString(strings.Repeat("\n", strings.Count(source[start:i+end+3], "\n")))
				i += end + 3
				continue
			}
			for i < len(source) {
				if source[i] == '\\' {
					i = min(i+2, len(source))
					continue
				}
				if source[i] == quote {
					i++
					break
				}
				i++
			}
			out.WriteString(source[start:i])
			continue
		}
		out.WriteByte(source[i])
		i++
	}
	return out.String()
}

func requiredEnv(name string) bool {
	switch name {
	case "DJANGO_SECRET_KEY", "DATABASE_URL", "REDIS_URL", "MONGO_URI", "MONGODB_URI", "MYSQL", "API_KEY", "TOKEN", "SECRET", "PASSWORD":
		return true
	}
	return strings.HasPrefix(name, "MYSQL_") || strings.HasSuffix(name, "_TOKEN") || strings.HasSuffix(name, "_SECRET") || strings.HasSuffix(name, "_API_KEY") || strings.HasSuffix(name, "_PASSWORD")
}
