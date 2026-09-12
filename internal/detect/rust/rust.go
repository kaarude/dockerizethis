// Package rust detects Cargo projects without executing project code.
package rust

import (
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
	"github.com/carl/dockerizethis/internal/detect/source"
	"github.com/carl/dockerizethis/internal/plan"
)

// Detector is ready to use without configuration.
type Detector struct{}

var _ detect.Detector = Detector{}

func (Detector) Name() string { return "rust" }

var (
	sectionPattern   = regexp.MustCompile(`^\s*\[\[?(.+?)\]\]?\s*$`)
	keyValuePattern  = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=\s*(.+?)\s*$`)
	stringPattern    = regexp.MustCompile(`^"([^"]*)"|^'([^']*)'`)
	toolchainPattern = regexp.MustCompile(`(?m)^\s*channel\s*=\s*"([^"]+)"`)
)

// dep is a declared dependency: its name plus the raw spec text, so feature
// lists such as sqlx's { features = ["postgres"] } stay visible.
type dep struct {
	name string
	spec string
}

type binary struct{ name, path string }

// manifest is the slice of Cargo.toml the detector understands.
type manifest struct {
	name        string   // [package] name
	bins        []binary // explicit [[bin]] targets
	defaultRun  string
	autobins    bool
	rustVersion string // [package] rust-version
	workspace   bool   // [workspace] section present
	hasPackage  bool   // [package] section present
	deps        []dep  // [dependencies] and [target.*.dependencies]
}

// parseManifest reads the sections and keys dockerize needs. It is a line
// scanner, not a TOML parser: multi-line arrays or dotted keys it cannot see
// simply do not contribute evidence.
func parseManifest(source string) manifest {
	m := manifest{autobins: true}
	section := ""
	depIndex := -1
	for _, line := range strings.Split(stripComments(source), "\n") {
		if match := sectionPattern.FindStringSubmatch(line); match != nil {
			section = match[1]
			depIndex = -1
			depName := ""
			if strings.HasPrefix(section, "dependencies.") {
				depName = strings.TrimPrefix(section, "dependencies.")
			} else if strings.HasPrefix(section, "target.") {
				if _, tail, ok := strings.Cut(section, ".dependencies."); ok {
					depName = tail
				}
			}
			if depName != "" {
				m.deps = append(m.deps, dep{name: strings.Trim(depName, "\"'")})
				depIndex = len(m.deps) - 1
			}
			switch section {
			case "package":
				m.hasPackage = true
			case "workspace":
				m.workspace = true
			case "bin":
				m.bins = append(m.bins, binary{})
			}
			continue
		}
		if depIndex >= 0 {
			m.deps[depIndex].spec += line + "\n"
			continue
		}
		kv := keyValuePattern.FindStringSubmatch(line)
		if kv == nil {
			continue
		}
		key, value := kv[1], kv[2]
		switch section {
		case "package":
			switch key {
			case "name":
				m.name = literal(value)
			case "default-run":
				m.defaultRun = literal(value)
			case "autobins":
				m.autobins = value != "false"
			case "rust-version":
				m.rustVersion = literal(value)
			}
		case "bin":
			if key == "path" {
				m.bins[len(m.bins)-1].path = literal(value)
			}
			if key == "name" {
				m.bins[len(m.bins)-1].name = literal(value)
			}
		case "dependencies":
			m.deps = append(m.deps, dep{name: key, spec: value})
		default:
			// Target-specific dependency tables such as [target.'cfg(unix)'.dependencies].
			if strings.HasPrefix(section, "target.") && strings.HasSuffix(section, ".dependencies") {
				m.deps = append(m.deps, dep{name: key, spec: value})
			}
		}
	}
	return m
}

// literal extracts a basic or literal TOML string's value.
func literal(value string) string {
	if match := stringPattern.FindStringSubmatch(value); match != nil {
		if match[1] != "" {
			return match[1]
		}
		return match[2]
	}
	return ""
}

// stripComments removes TOML comments while preserving quoted strings.
func stripComments(source string) string {
	var out strings.Builder
	for i := 0; i < len(source); {
		c := source[i]
		if c == '"' || c == '\'' {
			start := i
			i++
			for i < len(source) && source[i] != c && source[i] != '\n' {
				if c == '"' && source[i] == '\\' {
					i = min(i+2, len(source))
					continue
				}
				i++
			}
			i = min(i+1, len(source))
			out.WriteString(source[start:i])
			continue
		}
		if c == '#' {
			for i < len(source) && source[i] != '\n' {
				i++
			}
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.String()
}

// Detect returns ok=false when dir has no Cargo.toml. A workspace root
// without [package] stays ok=true with a low-confidence plan that needs
// Extras["binPackage"] before rendering.
func (Detector) Detect(dir string) (plan.Plan, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if errors.Is(err, fs.ErrNotExist) {
		return plan.Plan{}, false, nil
	}
	if err != nil {
		return plan.Plan{}, false, fmt.Errorf("read Cargo.toml: %w", err)
	}
	m := parseManifest(string(data))
	if !m.hasPackage && !m.workspace {
		return plan.Plan{}, false, fmt.Errorf("parse Cargo.toml: missing [package] or [workspace] section")
	}
	p := plan.Plan{Stack: "rust", PkgManager: "cargo", Process: plan.ProcessWorker, Workdir: "/app", BuildCmd: "cargo build --release", Confidence: 0.6}

	p.Version = m.rustVersion
	if p.Version == "" {
		p.Version, err = toolchainVersion(dir)
		if err != nil {
			return plan.Plan{}, false, err
		}
	}
	if p.Version == "" {
		p.Version = "1"
		p.Notes = append(p.Notes, "no rust-version or toolchain channel; using latest stable Rust")
	}

	bin, err := binaryTarget(dir, m)
	if err != nil {
		return plan.Plan{}, false, err
	}
	if bin != "" {
		p.StartCmd = bin
		p.Extras = map[string]string{"bin": bin}
		p.BuildCmd += " --bin " + bin
	}

	deps := map[string]string{}
	for _, d := range m.deps {
		deps[d.name] += "\n" + d.spec
	}
	for _, framework := range []struct{ name, crate string }{
		{"axum", "axum"}, {"actix-web", "actix-web"}, {"rocket", "rocket"},
		{"warp", "warp"}, {"poem", "poem"}, {"tide", "tide"}, {"salvo", "salvo"},
	} {
		if _, ok := deps[framework.crate]; ok {
			p.Framework = framework.name
			break
		}
	}
	if _, ok := deps["hyper"]; ok && p.Framework == "" {
		p.Framework = "hyper"
	}

	// Loose port patterns such as .port(9000) only apply once a web framework
	// is detected; in a worker they would match database client configs.
	names, port, health, err := scanSources(dir, p.Framework != "")
	if err != nil {
		return plan.Plan{}, false, err
	}
	p.HealthPath = health
	if p.Framework != "" {
		p.Process = plan.ProcessWeb
		p.Port = port
		if p.Port == 0 {
			switch p.Framework {
			case "axum":
				p.Port = 3000
			case "rocket":
				p.Port = 8000
			default:
				p.Port = 8080
			}
		}
		p.Confidence = 0.9
	} else {
		p.HealthPath = ""
		if port != 0 {
			p.Notes = append(p.Notes, fmt.Sprintf("found port %d in sources but no web framework; treated as a worker", port))
		}
	}
	if bin == "" {
		p.Process, p.Port, p.HealthPath, p.Confidence = "", 0, "", 0.4
		p.Notes = append(p.Notes, "no unambiguous executable target; select a workspace member or set package.default-run for multiple binaries")
	}

	for _, service := range []struct {
		name plan.Service
		ok   bool
	}{
		{plan.ServicePostgres, hasFeature(deps["sqlx"], "postgres") || hasFeature(deps["diesel"], "postgres") ||
			hasFeature(deps["sea-orm"], "sqlx-postgres") || anyDep(deps, "tokio-postgres", "postgres", "deadpool-postgres", "bb8-postgres")},
		{plan.ServiceMySQL, hasFeature(deps["sqlx"], "mysql") || hasFeature(deps["diesel"], "mysql") ||
			hasFeature(deps["sea-orm"], "sqlx-mysql") || anyDep(deps, "mysql_async", "mysql", "mysql_common")},
		{plan.ServiceRedis, anyDep(deps, "redis", "deadpool-redis", "bb8-redis", "fred")},
		{plan.ServiceMongo, anyDep(deps, "mongodb", "wither")},
	} {
		if service.ok {
			p.Services = append(p.Services, service.name)
		}
	}
	slices.Sort(p.Services)

	for name := range names {
		p.Env = append(p.Env, plan.EnvVar{Name: name, Required: requiredEnv(name)})
	}
	slices.SortFunc(p.Env, func(a, b plan.EnvVar) int { return strings.Compare(a.Name, b.Name) })

	var native []string
	if anyDep(deps, "openssl", "openssl-sys", "native-tls") {
		native = append(native, "ssl")
	}
	if anyDep(deps, "pq-sys", "libpq-sys") {
		native = append(native, "pq")
	}
	if anyDep(deps, "mysqlclient-sys") {
		native = append(native, "mysqlclient")
	}
	if len(native) > 0 {
		if p.Extras == nil {
			p.Extras = map[string]string{}
		}
		slices.Sort(native)
		p.Extras["nativeDeps"] = strings.Join(native, " ")
		p.Notes = append(p.Notes, "native libraries detected ("+strings.Join(native, ", ")+"); confirm the runtime image has every library the crates need")
	}
	return p, true, nil
}

// binaryTarget follows Cargo's conventional target names. Ambiguous packages
// need default-run instead of silently selecting an arbitrary executable.
func binaryTarget(dir string, m manifest) (string, error) {
	if !m.hasPackage {
		return "", nil
	}
	var names []string
	claimed := map[string]bool{}
	for _, bin := range m.bins {
		if bin.name != "" {
			names = append(names, bin.name)
		}
		if bin.path != "" {
			claimed[filepath.ToSlash(filepath.Clean(bin.path))] = true
		}
	}
	if m.autobins {
		if info, err := os.Stat(filepath.Join(dir, "src/main.rs")); err == nil && info.Mode().IsRegular() {
			if !claimed["src/main.rs"] {
				names = append(names, m.name)
			}
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		entries, err := os.ReadDir(filepath.Join(dir, "src/bin"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		for _, entry := range entries {
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".rs") && !claimed["src/bin/"+entry.Name()] {
				names = append(names, strings.TrimSuffix(entry.Name(), ".rs"))
			}
			if entry.IsDir() && !claimed["src/bin/"+entry.Name()+"/main.rs"] {
				info, err := os.Stat(filepath.Join(dir, "src/bin", entry.Name(), "main.rs"))
				if err == nil && info.Mode().IsRegular() {
					names = append(names, entry.Name())
				} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
					return "", err
				}
			}
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	if m.defaultRun != "" {
		if slices.Contains(names, m.defaultRun) {
			return m.defaultRun, nil
		}
		return "", nil
	}
	if len(names) == 1 {
		return names[0], nil
	}
	return "", nil
}

func anyDep(deps map[string]string, names ...string) bool {
	for _, name := range names {
		if _, ok := deps[name]; ok {
			return true
		}
	}
	return false
}

// hasFeature reports whether a dependency spec lists feature in its
// features = [...] array or in dep = { workspace = true, features = [...] }.
func hasFeature(spec, feature string) bool {
	if spec == "" {
		return false
	}
	quoted := regexp.QuoteMeta(feature)
	pattern := regexp.MustCompile(`features\s*=\s*\[[^\]]*"` + quoted + `"`)
	return pattern.MatchString(spec)
}

// toolchainVersion reads rust-toolchain.toml or rust-toolchain. Only bare
// version numbers such as 1.85 are usable as image tags; named channels fall
// through to the default so the generated tag is always valid.
var numericVersion = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}$`)

func toolchainVersion(dir string) (string, error) {
	for _, name := range []string{"rust-toolchain.toml", "rust-toolchain"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		channel := strings.TrimSpace(string(data))
		if strings.HasSuffix(name, ".toml") {
			if match := toolchainPattern.FindStringSubmatch(string(data)); match != nil {
				channel = match[1]
			}
		}
		// Channels may carry a date or components, e.g. nightly-2025-01-01.
		channel = strings.SplitN(channel, "-", 2)[0]
		if numericVersion.MatchString(channel) {
			return channel, nil
		}
		return "", nil
	}
	return "", nil
}

var (
	envPattern           = regexp.MustCompile(`\b(?:std::)?env::(?:var|var_os)\s*\(\s*"([A-Z_][A-Z0-9_]*)"`)
	portAddrPattern      = regexp.MustCompile(`(?:0\.0\.0\.0|127\.0\.0\.1|::1|localhost):([0-9]{2,5})`)
	portTuplePattern     = regexp.MustCompile(`\(\s*(?:\[\s*0\s*,\s*0\s*,\s*0\s*,\s*0\s*\]|"[^"]*"|Ipv4Addr::UNSPECIFIED|Ipv6Addr::UNSPECIFIED|std::net::Ipv4Addr::UNSPECIFIED)\s*,\s*([0-9]{2,5})\s*\)`)
	portCallPattern      = regexp.MustCompile(`\.port\s*\(\s*([0-9]{2,5})\s*\)`)
	portFieldPattern     = regexp.MustCompile(`\bport\s*:\s*([0-9]{2,5})`)
	portEnvPattern       = regexp.MustCompile(`env::var\s*\(\s*"PORT"\s*\)[^\n;]*?"([0-9]{2,5})"`)
	healthBlockedPattern = regexp.MustCompile(`#\s*\[\s*(?:cfg|test|tokio::test)|\.\s*(?:nest|nest_service|merge)\s*\(`)
	healthCallPattern    = regexp.MustCompile(`\.route\s*\(\s*"(/health[A-Za-z0-9_/-]*)"\s*,\s*(?:axum::routing::)?get\s*\(`)
)

// scanSources walks .rs files for env::var names, a literal listen port, and
// a /health route. target/, .git, and non-regular entries are skipped.
func scanSources(dir string, loosePorts bool) (map[string]bool, int, string, error) {
	names := map[string]bool{}
	port := 0
	health := ""
	healthBlocked := false
	patterns := []*regexp.Regexp{portAddrPattern, portTuplePattern, portEnvPattern}
	if loosePorts {
		patterns = []*regexp.Regexp{portAddrPattern, portTuplePattern, portCallPattern, portFieldPattern, portEnvPattern}
	}
	err := filepath.WalkDir(dir, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(dir, filename)
		if err != nil {
			return err
		}
		if source.IsTestPath(relative) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if filename != dir {
				switch entry.Name() {
				case "target", ".git", "vendor":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !entry.Type().IsRegular() || filepath.Ext(filename) != ".rs" {
			return nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		lex := source.Read(string(data))
		if healthBlockedPattern.MatchString(lex.Code) {
			healthBlocked = true
		}
		data = []byte(lex.Literal)
		for _, match := range envPattern.FindAllSubmatch(data, -1) {
			names[string(match[1])] = true
		}
		if port == 0 {
			for _, pattern := range patterns {
				if match := pattern.FindSubmatch(data); match != nil {
					if n, err := strconv.Atoi(string(match[1])); err == nil && n > 0 && n <= 65535 {
						port = n
						break
					}
				}
			}
		}
		if health == "" && strings.Contains(lex.Code, "Router::new") {
			if matches := lex.Matches(healthCallPattern); len(matches) > 0 {
				health = matches[0][1]
			}
		}

		return nil
	})
	if err != nil {
		return nil, 0, "", fmt.Errorf("scan Rust sources: %w", err)
	}
	if healthBlocked {
		health = ""
	}
	return names, port, health, nil
}

func requiredEnv(name string) bool {
	switch name {
	case "DATABASE_URL", "REDIS_URL", "MONGO_URI", "MONGODB_URI", "MYSQL", "API_KEY", "TOKEN", "SECRET", "PASSWORD":
		return true
	}
	return strings.HasPrefix(name, "MYSQL_") || strings.HasSuffix(name, "_TOKEN") ||
		strings.HasSuffix(name, "_SECRET") || strings.HasSuffix(name, "_API_KEY") || strings.HasSuffix(name, "_PASSWORD")
}
