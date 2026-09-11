// Package node detects Node.js projects without executing their code or scripts.
package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/carl/dockerizethis/internal/detect"
	"github.com/carl/dockerizethis/internal/plan"
)

// Detector is ready to use without configuration.
type Detector struct{}

var _ detect.Detector = Detector{}

func (Detector) Name() string { return "node" }

type manifest struct {
	Main            string            `json:"main"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Engines         struct {
		Node string `json:"node"`
	} `json:"engines"`
}

func (m manifest) has(names ...string) bool {
	for _, name := range names {
		if _, ok := m.Dependencies[name]; ok {
			return true
		}
		if _, ok := m.DevDependencies[name]; ok {
			return true
		}
	}
	return false
}

// Detect returns a partial plan with notes when a package has no clear process.
// scripts.start is preserved verbatim; main becomes a quoted node invocation.
func (Detector) Detect(dir string) (plan.Plan, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return plan.Plan{}, false, nil
	}
	if err != nil {
		return plan.Plan{}, false, fmt.Errorf("read package.json: %w", err)
	}
	var m *manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return plan.Plan{}, false, fmt.Errorf("parse package.json: %w", err)
	}
	if m == nil {
		return plan.Plan{}, false, fmt.Errorf("parse package.json: expected an object")
	}

	p := plan.Plan{Stack: "node", Workdir: "/app"}
	var locked, versioned bool
	var lockfile string
	p.PkgManager, lockfile, err = packageManager(dir)
	locked = lockfile != ""
	if lockfile == "bun.lock" || lockfile == "npm-shrinkwrap.json" {
		p.Extras = map[string]string{"lockfile": lockfile}
	}
	if err != nil {
		return plan.Plan{}, false, err
	}
	if lockfile == "" {
		// "none" tells the renderer there is no lockfile to COPY; only npm
		// can install without one.
		if p.Extras == nil {
			p.Extras = map[string]string{}
		}
		p.Extras["lockfile"] = "none"
		p.Notes = append(p.Notes, "no lockfile found; npm install is not reproducible — commit package-lock.json")
	}
	p.Version, versioned, err = nodeVersion(dir, m.Engines.Node)
	if err != nil {
		return plan.Plan{}, false, err
	}
	for _, name := range []string{"next", "nuxt", "express", "fastify", "koa", "hono", "astro"} {
		if m.has(name) {
			p.Framework = name
			break
		}
	}
	start := strings.TrimSpace(m.Scripts["start"])
	p.StartCmd = start
	if start == "" && strings.TrimSpace(m.Main) != "" {
		// Prefix relative paths so a main beginning with '-' cannot be a Node flag.
		entry := m.Main
		if !strings.HasPrefix(entry, "/") && !strings.HasPrefix(entry, "./") {
			entry = "./" + entry
		}
		p.StartCmd = "node '" + strings.ReplaceAll(entry, "'", "'\"'\"'") + "'"
	}
	build := strings.TrimSpace(m.Scripts["build"]) != ""
	if build {
		p.BuildCmd = "npm run build"
	}
	server := m.has("next", "nuxt", "express", "fastify", "koa", "hono")
	worker := m.has("discord.js", "telegraf", "bullmq")
	static := m.has("vite", "react-scripts", "astro")
	switch {
	case server && start != "":
		p.Process = plan.ProcessWeb
	case !server && worker:
		p.Process = plan.ProcessWorker
	case !server && static && build:
		p.Process = plan.ProcessStatic
	case !server && longRunning(p.StartCmd):
		p.Process = plan.ProcessWorker
	default:
		p.Notes = append(p.Notes, "could not determine process type; confirm how this package runs")
	}
	if p.StartCmd == "" {
		p.Notes = append(p.Notes, "no start script or main field; provide a start command")
	}

	scan, err := scanSources(dir, p.Framework)
	if err != nil {
		return plan.Plan{}, false, err
	}
	p.Env, p.Port = scan.env, scan.port
	if p.Port == 0 {
		switch {
		case p.Framework == "next" || p.Framework == "nuxt":
			p.Port = 3000
		case p.Process == plan.ProcessStatic && m.has("vite"):
			p.Port = 4173
		case p.Process == plan.ProcessStatic:
			// The generated runtime is nginx, which needs a valid listen port.
			p.Port = 8080
		case p.Process == plan.ProcessWeb:
			p.Port = scan.listenPort
		}
	}
	if p.Process == plan.ProcessWeb {
		p.HealthPath = scan.healthPath
		if p.Port == 0 {
			p.Notes = append(p.Notes, "could not detect a listen port; use process.env.PORT with a default or a literal .listen(port) call")
		}
	}
	// An explicit react-scripts build still emits build/ when Vite is also installed.
	buildFields := strings.Fields(m.Scripts["build"])
	reactScriptsBuild := len(buildFields) >= 2 && buildFields[0] == "react-scripts" && buildFields[1] == "build"
	if p.Process == plan.ProcessStatic && m.has("react-scripts") && (!m.has("vite") || reactScriptsBuild) {
		if p.Extras == nil {
			p.Extras = map[string]string{}
		}
		p.Extras["staticDir"] = "build"
	}
	p.Services = services(*m, p.Env)
	if m.has("prisma", "@prisma/client") {
		if !slices.Contains(p.Services, plan.ServicePostgres) {
			p.Services = append(p.Services, plan.ServicePostgres)
			slices.Sort(p.Services)
		}
		p.Notes = append(p.Notes, "prisma detected — confirm database and run migrations outside the container")
	}

	// Count independent evidence categories once each, never individual matches.
	// Defaults contribute no evidence. Confidence is a heuristic, capped at 1.
	score := 5
	for _, signal := range []bool{locked, versioned, p.Framework != "" || worker || static,
		p.StartCmd != "", build, len(p.Env) > 0, len(p.Services) > 0} {
		if signal {
			score++
		}
	}
	p.Confidence = float64(min(score, 10)) / 10
	return p, true, nil
}

func packageManager(dir string) (string, string, error) {
	for _, lock := range []struct{ file, manager string }{
		{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"},
		{"bun.lock", "bun"}, {"bun.lockb", "bun"},
		{"npm-shrinkwrap.json", "npm"}, {"package-lock.json", "npm"},
	} {
		info, err := os.Stat(filepath.Join(dir, lock.file))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", fmt.Errorf("inspect %s: %w", lock.file, err)
		}
		if info.Mode().IsRegular() {
			return lock.manager, lock.file, nil
		}
	}
	return "npm", "", nil
}

// Common exact versions and lower-bound engine ranges yield a numeric image tag.
// Unsupported selectors (such as lts/* or upper bounds) fall through to the next source.
var versionPattern = regexp.MustCompile(`^(?:>=\s*|[~^=]\s*)?v?([0-9]+(?:\.[0-9]+){0,2})(?:$|[\s.xX*|])`)

func nodeVersion(dir, engine string) (string, bool, error) {
	if match := versionPattern.FindStringSubmatch(strings.TrimSpace(engine)); match != nil {
		return match[1], true, nil
	}
	for _, name := range []string{".nvmrc", ".node-version"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("read %s: %w", name, err)
		}
		if match := versionPattern.FindStringSubmatch(strings.TrimSpace(string(data))); match != nil {
			return match[1], true, nil
		}
	}
	return "20", false, nil
}

// A runtime plus an entry point is evidence of a worker, not proof of longevity.
// Avoid classifying obviously one-shot commands such as echo, builds or node -e.
func longRunning(command string) bool {
	fields := strings.Fields(command)
	for len(fields) > 0 && (fields[0] == "env" || fields[0] == "cross-env" || strings.Contains(fields[0], "=")) {
		fields = fields[1:]
	}
	if len(fields) < 2 {
		return false
	}
	switch filepath.Base(fields[0]) {
	case "node", "tsx", "ts-node", "bun", "nodemon":
		entryPoint := false
		for _, arg := range fields[1:] {
			arg = strings.Trim(arg, "'\"")
			switch arg {
			case "-e", "--eval", "-p", "--print", "--test", "--version", "-v", "--help", "-h":
				return false
			}
			if strings.HasPrefix(arg, "--eval=") || strings.HasPrefix(arg, "--print=") {
				return false
			}
			if !strings.HasPrefix(arg, "-") && (strings.HasSuffix(arg, ".js") || strings.HasSuffix(arg, ".ts") ||
				strings.HasSuffix(arg, ".mjs") || strings.HasSuffix(arg, ".cjs")) {
				entryPoint = true
			}
		}
		return entryPoint
	}
	return false
}

var envPattern = regexp.MustCompile(`process\.env\.([A-Z_][A-Z0-9_]*)\b`)
var portPattern = regexp.MustCompile(`process\.env\.PORT\b\s*(?:,\s*10\s*)?\)*\s*(?:\|\||\?\?)\s*["']?([0-9]+)\b`)

// A literal listen(port) is weaker evidence than a process.env.PORT fallback:
// it applies only when nothing else yields a port for a web process.
var listenPattern = regexp.MustCompile(`\.listen\s*\(\s*([0-9]{1,5})\b`)
var healthRoutePattern = regexp.MustCompile(`\.(?:get|route|use|all)\s*\(\s*['"` + "`" + `](/healthz?)/?['"` + "`" + `]`)

// sourceScan collects source evidence in a single walk; the first valid match
// in lexical file order wins for port, listenPort, and healthPath.
type sourceScan struct {
	env        []plan.EnvVar
	port       int
	listenPort int
	healthPath string
}

func scanSources(dir string, framework string) (sourceScan, error) {
	var scan sourceScan
	names := make(map[string]bool)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "node_modules", "dist", "build", ".git":
				if path != dir {
					return filepath.SkipDir
				}
			}
			return nil
		}
		// Do not follow source symlinks into other projects or read special files.
		if !entry.Type().IsRegular() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".ts", ".js", ".mjs", ".cjs", ".tsx", ".jsx":
		default:
			return nil
		}
		if scan.healthPath == "" && (framework == "next" || framework == "nuxt") {
			scan.healthPath = fileRouteHealthPath(dir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range envPattern.FindAllSubmatch(data, -1) {
			names[string(match[1])] = true
		}
		for _, match := range portPattern.FindAllSubmatch(data, -1) {
			value, err := strconv.Atoi(string(match[1]))
			if err == nil && value > 0 && value <= 65535 && scan.port == 0 {
				scan.port = value
			}
		}
		for _, match := range listenPattern.FindAllSubmatch(data, -1) {
			value, err := strconv.Atoi(string(match[1]))
			if err == nil && value > 0 && value <= 65535 && scan.listenPort == 0 {
				scan.listenPort = value
			}
		}
		if scan.healthPath == "" {
			if match := healthRoutePattern.FindSubmatch(data); match != nil {
				scan.healthPath = string(match[1])
			}
		}
		return nil
	})
	if err != nil {
		return sourceScan{}, fmt.Errorf("scan Node sources: %w", err)
	}
	for name := range names {
		scan.env = append(scan.env, plan.EnvVar{Name: name, Required: requiredEnv(name)})
	}
	slices.SortFunc(scan.env, func(a, b plan.EnvVar) int { return strings.Compare(a.Name, b.Name) })
	return scan, nil
}

// fileRouteHealthPath maps Next and Nuxt file-based routes such as
// app/api/health/route.ts or pages/health.tsx onto their URL path.
func fileRouteHealthPath(dir, filename string) string {
	rel, err := filepath.Rel(dir, filename)
	if err != nil {
		return ""
	}
	segments := strings.Split(filepath.ToSlash(rel), "/")
	file := segments[len(segments)-1]
	segments = segments[:len(segments)-1]
	marker := -1
	for i, segment := range segments {
		if segment == "app" || segment == "pages" {
			marker = i
		}
	}
	if marker < 0 {
		return ""
	}
	route := slices.Clone(segments[marker+1:])
	stem := strings.TrimSuffix(file, filepath.Ext(file))
	switch stem {
	case "route", "page", "index":
	case "health", "healthz":
		// Only the pages router turns a file named health into a URL path;
		// app/health.ts is an ordinary module there.
		if segments[marker] != "pages" {
			return ""
		}
		route = append(route, stem)
	default:
		return ""
	}
	if len(route) == 0 || (route[len(route)-1] != "health" && route[len(route)-1] != "healthz") {
		return ""
	}
	return "/" + strings.Join(route, "/")
}

func requiredEnv(name string) bool {
	switch name {
	case "DATABASE_URL", "REDIS_URL", "MONGO_URI", "MONGODB_URI", "MYSQL", "API_KEY", "TOKEN", "SECRET", "PASSWORD":
		return true
	}
	return strings.HasPrefix(name, "MYSQL_") || strings.HasSuffix(name, "_TOKEN") ||
		strings.HasSuffix(name, "_SECRET") || strings.HasSuffix(name, "_API_KEY") || strings.HasSuffix(name, "_PASSWORD")
}

// Require matching environment and dependency evidence, except for Prisma above.
func services(m manifest, env []plan.EnvVar) []plan.Service {
	var result []plan.Service
	for _, variable := range env {
		var service plan.Service
		switch {
		case variable.Name == "DATABASE_URL" && m.has("pg"):
			service = plan.ServicePostgres
		case variable.Name == "REDIS_URL" && m.has("ioredis"):
			service = plan.ServiceRedis
		case (variable.Name == "MONGO_URI" || variable.Name == "MONGODB_URI") && m.has("mongoose"):
			service = plan.ServiceMongo
		case (variable.Name == "MYSQL" || strings.HasPrefix(variable.Name, "MYSQL_")) && m.has("mysql2"):
			service = plan.ServiceMySQL
		}
		if service != "" && !slices.Contains(result, service) {
			result = append(result, service)
		}
	}
	slices.Sort(result)
	return result
}
