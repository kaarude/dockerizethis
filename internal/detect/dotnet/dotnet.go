// Package dotnet detects .NET projects without executing project code or
// dotnet tooling.
package dotnet

import (
	"encoding/xml"
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

func (Detector) Name() string { return "dotnet" }

// projectFile is the slice of a .csproj/.fsproj/.vbproj the detector reads.
// encoding/xml matches local names regardless of the MSBuild namespace.
type projectFile struct {
	Sdk            string `xml:"Sdk,attr"`
	PropertyGroups []struct {
		TargetFramework  string `xml:"TargetFramework"`
		TargetFrameworks string `xml:"TargetFrameworks"`
		OutputType       string `xml:"OutputType"`
		AssemblyName     string `xml:"AssemblyName"`
	} `xml:"PropertyGroup"`
	ItemGroups []struct {
		PackageReferences []struct {
			Include string `xml:"Include,attr"`
		} `xml:"PackageReference"`
		FrameworkReferences []struct {
			Include string `xml:"Include,attr"`
		} `xml:"FrameworkReference"`
	} `xml:"ItemGroup"`
}

var (
	assemblyPattern      = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	healthBlockedPattern = regexp.MustCompile(`\.\s*(?:MapGroup|UsePathBase|Map)\s*\(|\[\s*(?:Fact|Theory|Test|TestMethod)\b`)
	slnProjectPattern    = regexp.MustCompile(`(?m)=\s*"[^"]+"\s*,\s*"([^"]+\.(?:cs|fs|vb)proj)"`)
	// Only net5.0-style monikers are image tags; net472 or netstandard2.0
	// fall through to the unrecognized-framework note.
	frameworkVersion  = regexp.MustCompile(`^net([5-9]\.[0-9]+|[1-9][0-9]+\.[0-9]+)$`)
	launchPortPattern = regexp.MustCompile(`"applicationUrl"\s*:\s*"[^"]*?http://[^":]+:([0-9]{2,5})`)
	envPattern        = regexp.MustCompile(`Environment\.GetEnvironmentVariable\s*\(\s*"([A-Z_][A-Z0-9_]*)"`)
	healthPattern     = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\.Map(?:Get|HealthChecks)\s*\(\s*"(/health[A-Za-z0-9_/-]*)"\s*[,)]`)
	appPattern        = regexp.MustCompile(`\b(?:var|WebApplication)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:WebApplication\.CreateBuilder\([^;]*|[A-Za-z_][A-Za-z0-9_]*\.)Build\s*\(`)
)

// packageServices maps PackageReference Include prefixes to backing services.
var packageServices = []struct {
	name     plan.Service
	prefixes []string
}{
	{plan.ServicePostgres, []string{"Npgsql"}},
	{plan.ServiceMySQL, []string{"MySql", "MySqlConnector", "Pomelo.EntityFrameworkCore.MySql"}},
	{plan.ServiceMongo, []string{"MongoDB"}},
	{plan.ServiceRedis, []string{"StackExchange.Redis", "Microsoft.Extensions.Caching.StackExchangeRedis"}},
}

// Detect returns ok=false when dir holds no .NET project or solution file.
// Project files live at the root or are discovered through a .sln; several
// candidates produce a low-confidence plan that needs Extras["projectFile"].
func (Detector) Detect(dir string) (plan.Plan, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return plan.Plan{}, false, fmt.Errorf("read directory: %w", err)
	}
	var projects, solutions []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".csproj"), strings.HasSuffix(name, ".fsproj"), strings.HasSuffix(name, ".vbproj"):
			projects = append(projects, name)
		case strings.HasSuffix(name, ".sln"):
			solutions = append(solutions, name)
		}
	}
	for _, sln := range solutions {
		data, err := os.ReadFile(filepath.Join(dir, sln))
		if err != nil {
			return plan.Plan{}, false, fmt.Errorf("read %s: %w", sln, err)
		}
		for _, match := range slnProjectPattern.FindAllStringSubmatch(string(data), -1) {
			// Solution entries use Windows path separators.
			projects = append(projects, strings.ReplaceAll(match[1], `\`, "/"))
		}
	}
	if len(projects) == 0 {
		return plan.Plan{}, false, nil
	}
	slices.Sort(projects)
	projects = slices.Compact(projects)

	p := plan.Plan{Stack: "dotnet", Version: "8.0", PkgManager: "dotnet", Process: plan.ProcessWorker, Workdir: "/app", Confidence: 0.8}
	chosen := ""
	if len(projects) == 1 {
		chosen = projects[0]
	} else {
		// An empty process surfaces the note as "could not determine how
		// this project runs" instead of a later render error.
		p.Process, p.Confidence = "", 0.4
		p.Notes = append(p.Notes, "multiple project files found ("+strings.Join(projects, ", ")+"); set Extras[\"projectFile\"] to the executable project")
	}
	if chosen == "" {
		return p, true, nil
	}
	chosen = filepath.ToSlash(filepath.Clean(filepath.FromSlash(chosen)))
	if !filepath.IsLocal(filepath.FromSlash(chosen)) {
		return plan.Plan{}, false, fmt.Errorf("project path must stay inside the build context: %s", chosen)
	}
	p.Extras = map[string]string{"projectFile": chosen}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return plan.Plan{}, false, err
	}
	defer func() { _ = root.Close() }()

	data, err := root.ReadFile(filepath.FromSlash(chosen))
	if err != nil {
		return plan.Plan{}, false, fmt.Errorf("read %s: %w", chosen, err)
	}
	var proj projectFile
	if err := xml.Unmarshal(data, &proj); err != nil {
		return plan.Plan{}, false, fmt.Errorf("parse %s: %w", chosen, err)
	}

	var tfm, outputType, assembly string
	multiTarget := false
	var packages, frameworks []string
	for _, group := range proj.PropertyGroups {
		if group.TargetFramework != "" && tfm == "" {
			tfm = group.TargetFramework
		}
		if group.TargetFrameworks != "" && tfm == "" {
			tfm = strings.TrimSpace(strings.SplitN(group.TargetFrameworks, ";", 2)[0])
			multiTarget = true
		}
		if group.OutputType != "" && outputType == "" {
			outputType = group.OutputType
		}
		if group.AssemblyName != "" && assembly == "" {
			assembly = group.AssemblyName
		}
	}
	for _, group := range proj.ItemGroups {
		for _, ref := range group.PackageReferences {
			packages = append(packages, ref.Include)
		}
		for _, ref := range group.FrameworkReferences {
			frameworks = append(frameworks, ref.Include)
		}
	}
	if match := frameworkVersion.FindStringSubmatch(strings.TrimSpace(tfm)); match != nil {
		p.Version = match[1]
	} else {
		p.Process, p.Confidence = "", 0.4
		p.Notes = append(p.Notes, "unsupported target framework "+tfm+"; select a Linux-compatible executable targeting net5.0 or later")
		return p, true, nil
	}
	if assembly == "" {
		assembly = strings.TrimSuffix(filepath.Base(chosen), filepath.Ext(chosen))
	}

	web := strings.Contains(proj.Sdk, ".Web")
	for _, name := range frameworks {
		if strings.HasPrefix(name, "Microsoft.AspNetCore") {
			web = true
		}
	}
	for _, name := range packages {
		if strings.HasPrefix(name, "Microsoft.AspNetCore") {
			web = true
		}
		for _, service := range packageServices {
			for _, prefix := range service.prefixes {
				if name == prefix || strings.HasPrefix(name, prefix+".") || strings.HasPrefix(name, prefix+"-") {
					p.Services = append(p.Services, service.name)
				}
			}
		}
	}
	slices.Sort(p.Services)
	p.Services = slices.Compact(p.Services)
	if web {
		p.Framework = "aspnet"
	}
	for _, name := range packages {
		for _, bot := range []string{"Discord.Net", "DSharpPlus", "NetCord"} {
			if strings.HasPrefix(name, bot) && p.Framework == "" {
				p.Framework = strings.ToLower(bot)
			}
		}
	}

	env, health, err := scanSources(filepath.Join(dir, filepath.Dir(chosen)))
	if err != nil {
		return plan.Plan{}, false, err
	}
	p.Env = env
	if web {
		p.Process = plan.ProcessWeb
		p.Port = launchSettingsPort(filepath.Join(dir, filepath.Dir(chosen)))
		if p.Port == 0 {
			p.Port = 8080
		}
		p.HealthPath = health
		p.Confidence = 0.9
	}
	if outputType == "Library" || (!web && outputType != "Exe" && outputType != "WinExe") {
		p.Process = ""
		p.Confidence = min(p.Confidence, 0.5)
		p.Notes = append(p.Notes, "project outputs a library; confirm the executable project to containerize")
	}
	p.BuildCmd = "dotnet publish " + chosen + " -c Release -o /app/out"
	if multiTarget {
		p.Extras["targetFramework"] = tfm
		p.BuildCmd += " --framework " + tfm
	}
	if !assemblyPattern.MatchString(assembly) {
		return plan.Plan{}, false, fmt.Errorf("unsupported assembly name %q", assembly)
	}
	p.StartCmd = "dotnet /app/" + assembly + ".dll"
	return p, true, nil
}

// launchSettingsPort reads the first HTTP applicationUrl port from
// Properties/launchSettings.json when present.
func launchSettingsPort(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, "Properties", "launchSettings.json"))
	if err != nil {
		return 0
	}
	if match := launchPortPattern.FindSubmatch(data); match != nil {
		if n, err := strconv.Atoi(string(match[1])); err == nil && n > 0 && n <= 65535 {
			return n
		}
	}
	return 0
}

// scanSources walks .cs and .fs files for GetEnvironmentVariable names and a
// mapped /health endpoint. bin/, obj/, and .git are skipped.
func scanSources(dir string) ([]plan.EnvVar, string, error) {
	names := map[string]bool{}
	health := ""
	healthBlocked := false
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
				case "bin", "obj", ".git", "out":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		switch filepath.Ext(filename) {
		case ".cs", ".fs":
		default:
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
		if health == "" && strings.Contains(lex.Code, "WebApplication.CreateBuilder") {
			for _, app := range lex.Matches(appPattern) {
				for _, route := range lex.Matches(healthPattern) {
					if route[1] == app[1] {
						health = route[2]
						break
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("scan .NET sources: %w", err)
	}
	var env []plan.EnvVar
	for name := range names {
		env = append(env, plan.EnvVar{Name: name, Required: requiredEnv(name)})
	}
	slices.SortFunc(env, func(a, b plan.EnvVar) int { return strings.Compare(a.Name, b.Name) })
	if healthBlocked {
		health = ""
	}
	return env, health, nil
}

func requiredEnv(name string) bool {
	switch name {
	case "DATABASE_URL", "REDIS_URL", "MONGO_URI", "MONGODB_URI", "MYSQL", "API_KEY", "TOKEN", "SECRET", "PASSWORD":
		return true
	}
	return strings.HasPrefix(name, "MYSQL_") || strings.HasSuffix(name, "_TOKEN") ||
		strings.HasSuffix(name, "_SECRET") || strings.HasSuffix(name, "_API_KEY") || strings.HasSuffix(name, "_PASSWORD")
}
