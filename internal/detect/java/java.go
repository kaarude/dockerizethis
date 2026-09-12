// Package java detects Maven and Gradle projects without executing project
// code or build scripts.
package java

import (
	"encoding/xml"
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

func (Detector) Name() string { return "java" }

var (
	javaVersionPattern    = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
	gradleDepPattern      = regexp.MustCompile(`(?:^|[\s(,])(?:implementation|api|compileOnly|runtimeOnly|annotationProcessor|kapt|classpath|id)\s*(?:\(\s*)?["']([^"':]+):([^"':]+)`)
	gradlePluginPattern   = regexp.MustCompile(`(?:id|kotlin)\s*\(?\s*["']([A-Za-z0-9_.-]+)["']|kotlin\s*\(\s*"([A-Za-z0-9_.-]+)"`)
	gradleVersionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`JavaVersion\.VERSION_([0-9]+)`),
		regexp.MustCompile(`JavaLanguageVersion\.of\(\s*([0-9]+)\s*\)`),
		regexp.MustCompile(`jvmToolchain\s*\(\s*([0-9]+)\s*\)`),
		regexp.MustCompile(`(?:source|target)Compatibility\s*=\s*["']?([0-9]+(?:\.[0-9]+)?)`),
	}
	serverPortPattern = regexp.MustCompile(`(?m)^\s*server\.port\s*[:=]\s*([0-9]{2,5})\s*$`)
	envPattern        = regexp.MustCompile(`System\.getenv(?:\(\s*\)\s*\.(?:get|getOrDefault)\s*)?\(\s*"([A-Z_][A-Z0-9_]*)"`)
	portEnvPattern    = regexp.MustCompile(`getOrDefault\(\s*"PORT"\s*,\s*"?([0-9]{2,5})`)
	ktorPortPattern   = regexp.MustCompile(`embeddedServer\s*\([^)]*\bport\s*=\s*([0-9]{1,5})\b`)
	portSocketPattern = regexp.MustCompile(`(?:InetSocketAddress|new ServerSocket)\s*\(\s*(?:"[^"]*"\s*,\s*)?([0-9]{2,5})`)
	javaMainPattern   = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)`)
	mainMethodPattern = regexp.MustCompile(`(?:public\s+static\s+void\s+main|static\s+void\s+main|fun\s+main)\s*\(`)
)

// webDeps are dependency coordinates that imply an HTTP server.
var webDeps = []string{
	"spring-boot-starter-web", "spring-boot-starter-webflux", "spring-boot-starter-tomcat",
	"spring-boot-starter-jetty", "spring-boot-starter-undertow",
	"quarkus-rest", "quarkus-resteasy", "quarkus-resteasy-reactive", "quarkus-vertx-http",
	"micronaut-http-server", "micronaut-server",
	"ktor-server", "javalin", "spark",
}

// frameworkDeps maps a framework name to dependency coordinates or plugin ids.
// Discord client libraries are frameworks too, but never imply web.
var frameworkDeps = []struct {
	name    string
	markers []string
}{
	{"spring-boot", []string{"org.springframework.boot", "spring-boot-starter"}},
	{"quarkus", []string{"io.quarkus"}},
	{"micronaut", []string{"io.micronaut"}},
	{"ktor", []string{"io.ktor"}},
	{"javalin", []string{"io.javalin"}},
	{"spark", []string{"com.sparkjava"}},
	{"JDA", []string{"net.dv8tion"}},
	{"discord4j", []string{"com.discord4j"}},
	{"javacord", []string{"org.javacord"}},
}

var serviceDeps = []struct {
	name    plan.Service
	markers []string
}{
	{plan.ServicePostgres, []string{"org.postgresql", "postgresql", "quarkus-jdbc-postgresql"}},
	{plan.ServiceMySQL, []string{"mysql-connector", "com.mysql", "org.mariadb", "mariadb-java-client", "quarkus-jdbc-mysql"}},
	{plan.ServiceMongo, []string{"org.mongodb", "mongodb-driver", "spring-boot-starter-data-mongodb", "quarkus-mongodb"}},
	{plan.ServiceRedis, []string{"redis.clients", "jedis", "lettuce", "spring-boot-starter-data-redis", "redisson", "quarkus-redis"}},
}

// Only active dependency declarations select backing services. A BOM, test
// dependency, or commented example does not describe a production dependency.
type coordinate struct {
	Group    string `xml:"groupId"`
	Artifact string `xml:"artifactId"`
	Scope    string `xml:"scope"`
}
type pomFile struct {
	Parent       coordinate   `xml:"parent"`
	Dependencies []coordinate `xml:"dependencies>dependency"`
	Plugins      []coordinate `xml:"build>plugins>plugin"`
	Properties   struct {
		Values []struct {
			XMLName xml.Name
			Value   string `xml:",chardata"`
		} `xml:",any"`
	} `xml:"properties"`
}

// Detect returns ok=false when neither a Maven pom nor Gradle build files
// exist. A Gradle project marker alone (settings.gradle, gradlew) counts.
func (Detector) Detect(dir string) (plan.Plan, bool, error) {
	pom, err := readOptional(dir, "pom.xml")
	if err != nil {
		return plan.Plan{}, false, err
	}
	var gradle string
	for _, name := range []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"} {
		data, err := readOptional(dir, name)
		if err != nil {
			return plan.Plan{}, false, err
		}
		if data != nil {
			gradle += "\n" + string(data)
		}
	}
	mvnw := exists(dir, "mvnw")
	gradlew := exists(dir, "gradlew")
	if pom == nil && gradle == "" && !mvnw && !gradlew {
		return plan.Plan{}, false, nil
	}

	p := plan.Plan{Stack: "java", Version: "21", Process: plan.ProcessWorker, Workdir: "/app", Confidence: 0.6}
	coords := map[string]bool{}
	if pom != nil {
		p.PkgManager = "maven"
		var parsed pomFile
		if err := xml.Unmarshal(pom, &parsed); err != nil {
			return plan.Plan{}, false, fmt.Errorf("parse pom.xml: %w", err)
		}
		for _, coord := range append(append(parsed.Dependencies, parsed.Plugins...), parsed.Parent) {
			if strings.TrimSpace(coord.Scope) == "test" || strings.TrimSpace(coord.Scope) == "import" {
				continue
			}
			if coord.Group != "" {
				coords[strings.TrimSpace(coord.Group)] = true
			}
			if coord.Artifact != "" {
				coords[strings.TrimSpace(coord.Artifact)] = true
			}
		}
		for _, key := range []string{"maven.compiler.release", "java.version", "maven.compiler.source", "maven.compiler.target"} {
			found := false
			for _, property := range parsed.Properties.Values {
				if property.XMLName.Local == key && javaVersionPattern.MatchString(strings.TrimSpace(property.Value)) {
					p.Version = strings.TrimSpace(property.Value)
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	gradle = source.Read(gradle).Literal
	if gradle != "" || (gradlew && pom == nil) {
		if p.PkgManager == "" || (gradlew && !mvnw) {
			p.PkgManager = "gradle"
		}
		for _, match := range gradleDepPattern.FindAllStringSubmatch(gradle, -1) {
			coords[match[1]] = true
			coords[match[2]] = true
		}
		for _, match := range gradlePluginPattern.FindAllStringSubmatch(gradle, -1) {
			id := match[1]
			if id == "" {
				id = match[2]
			}
			coords[id] = true
		}
		for _, pattern := range gradleVersionPatterns {
			if match := pattern.FindStringSubmatch(gradle); match != nil {
				p.Version = match[1]
				break
			}
		}
	}
	if pom != nil && (gradle != "" || gradlew) {
		if p.PkgManager == "maven" {
			p.Notes = append(p.Notes, "both Maven and Gradle files found; using Maven")
		} else {
			p.Notes = append(p.Notes, "both Maven and Gradle files found; using Gradle")
		}
	}
	if strings.HasPrefix(p.Version, "1.") {
		p.Version = strings.TrimPrefix(p.Version, "1.")
	}
	switch p.PkgManager {
	case "maven":
		if mvnw {
			p.BuildCmd = "./mvnw -B -DskipTests package"
		} else {
			p.BuildCmd = "mvn -B -DskipTests package"
		}
	default:
		if gradlew {
			p.BuildCmd = "./gradlew --no-daemon build -x test"
		} else {
			p.BuildCmd = "gradle --no-daemon build -x test"
		}
	}

	for _, framework := range frameworkDeps {
		if hasCoord(coords, framework.markers...) {
			p.Framework = framework.name
			break
		}
	}

	env, port, jdkHTTP, mainClass, kotlin, err := scanSources(dir)
	if err != nil {
		return plan.Plan{}, false, err
	}
	p.Env = env
	if mainClass == "" {
		mainClass = gradleMainClass(gradle)
	}
	if kotlin {
		kotlinPlugin := false
		for coord := range coords {
			if strings.Contains(coord, "kotlin") {
				kotlinPlugin = true
				break
			}
		}
		if !kotlinPlugin {
			p.Notes = append(p.Notes, "Kotlin sources detected; ensure the build applies the Kotlin plugin")
		}
	}
	if jdkHTTP && p.Framework == "" {
		p.Framework = "jdk-httpserver"
	}
	// Only web-specific coordinates or an HTTP server in source imply web;
	// a framework like spring-boot also ships CLI and scheduler apps.
	if hasCoord(coords, webDeps...) || jdkHTTP {
		p.Process, p.Confidence = plan.ProcessWeb, 0.9
		p.Port = port
		if p.Port == 0 {
			p.Port = 8080
		}
		p.HealthPath, err = healthPath(dir)
		if err != nil {
			return plan.Plan{}, false, err
		}
	}

	for _, service := range serviceDeps {
		if hasCoord(coords, service.markers...) {
			p.Services = append(p.Services, service.name)
		}
	}
	slices.Sort(p.Services)

	switch {
	case p.Framework == "quarkus":
		p.StartCmd = "java -jar /app/quarkus-app/quarkus-run.jar"
	case slices.Contains([]string{"spring-boot", "micronaut"}, p.Framework):
		// These frameworks repackage the jar into a self-contained executable.
		p.StartCmd = "java -jar /app/app.jar"
	case mainClass != "":
		p.StartCmd = "java -cp /app/app.jar " + mainClass
		if len(coords) > 0 && !hasCoord(coords, "maven-shade-plugin", "maven-assembly-plugin", "spring-boot-maven-plugin", "shadow") {
			p.Notes = append(p.Notes, "the packaged jar may not be self-contained; configure a shade/assembly/shadow plugin so dependencies are included")
		}
	default:
		p.StartCmd = "java -jar /app/app.jar"
		p.Confidence = 0.5
		p.Notes = append(p.Notes, "no main class found; confirm the jar manifest and start command")
	}
	return p, true, nil
}

var gradleMainPattern = regexp.MustCompile(`mainClass(?:Name)?\s*(?:=|\s)\s*["']([A-Za-z_][A-Za-z0-9_.$]*)["']`)

// gradleMainClass reads an explicit mainClass property from a Gradle build
// file, the convention set by the application or Kotlin plugins.
func gradleMainClass(text string) string {
	if match := gradleMainPattern.FindStringSubmatch(text); match != nil {
		return match[1]
	}
	return ""
}

// hasCoord reports whether any coordinate equals a marker or extends it with
// a name boundary, so marker "spark" matches spark-core but not sparkline.
func hasCoord(coords map[string]bool, markers ...string) bool {
	for coord := range coords {
		for _, marker := range markers {
			if coord == marker || strings.HasPrefix(coord, marker+".") || strings.HasPrefix(coord, marker+"-") || strings.HasPrefix(coord, marker+"_") {
				return true
			}
		}
	}
	return false
}

func exists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && info.Mode().IsRegular()
}

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

// scanSources walks .java and .kt files for System.getenv names, a listen
// port, com.sun.net.httpserver usage, and a main class or top-level fun main.
// target/, build/, .gradle/, and .git are skipped.
func scanSources(dir string) ([]plan.EnvVar, int, bool, string, bool, error) {
	names := map[string]bool{}
	port := 0
	jdkHTTP := false
	mainClass := ""
	kotlin := false
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
				case "target", "build", ".gradle", ".git", "out":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		ext := filepath.Ext(filename)
		if ext != ".java" && ext != ".kt" {
			return nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		source := source.Read(string(data)).Literal
		if ext == ".kt" {
			kotlin = true
		}
		for _, match := range envPattern.FindAllStringSubmatch(source, -1) {
			names[match[1]] = true
		}
		if strings.Contains(source, "com.sun.net.httpserver") || strings.Contains(source, "HttpServer.create(") {
			jdkHTTP = true
		}
		if port == 0 {
			for _, pattern := range []*regexp.Regexp{portEnvPattern, portSocketPattern, ktorPortPattern} {
				if match := pattern.FindStringSubmatch(source); match != nil {
					if n, err := strconv.Atoi(match[1]); err == nil && n > 0 && n <= 65535 {
						port = n
						break
					}
				}
			}
		}
		if mainClass == "" && mainMethodPattern.MatchString(source) {
			pkg := ""
			if match := javaMainPattern.FindStringSubmatch(source); match != nil {
				pkg = match[1]
			}
			class := strings.TrimSuffix(entry.Name(), ext)
			if ext == ".kt" {
				class += "Kt"
			}
			if pkg != "" {
				mainClass = pkg + "." + class
			} else {
				mainClass = class
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, false, "", false, fmt.Errorf("scan Java sources: %w", err)
	}
	var env []plan.EnvVar
	for name := range names {
		env = append(env, plan.EnvVar{Name: name, Required: requiredEnv(name)})
	}
	slices.SortFunc(env, func(a, b plan.EnvVar) int { return strings.Compare(a.Name, b.Name) })
	if port == 0 {
		port = configuredPort(dir)
	}
	return env, port, jdkHTTP, mainClass, kotlin, nil
}

// configuredPort reads server.port from application configuration files.
func configuredPort(dir string) int {
	for _, name := range []string{
		"src/main/resources/application.properties",
		"src/main/resources/application.yml",
		"src/main/resources/application.yaml",
		"application.properties",
	} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || len(data) > 1<<20 {
			continue
		}
		if match := serverPortPattern.FindSubmatch(data); match != nil {
			if n, err := strconv.Atoi(string(match[1])); err == nil && n > 0 && n <= 65535 {
				return n
			}
		}
	}
	return 0
}

// healthPath recognizes direct JDK HTTP contexts. Annotation and nested router
// paths need framework scope resolution and deliberately remain unspecified.
var contextPattern = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\.createContext\s*\(\s*"(/health[A-Za-z0-9_/-]*)"\s*,`)
var serverPattern = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*=\s*HttpServer\.create\s*\(`)

func healthPath(dir string) (string, error) {
	health := ""
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
			switch entry.Name() {
			case "target", "build", ".gradle", ".git", "out":
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || (filepath.Ext(filename) != ".java" && filepath.Ext(filename) != ".kt") {
			return nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		lex := source.Read(string(data))
		if strings.Contains(lex.Code, "getRequestMethod") || strings.Contains(lex.Code, "@Test") {
			return nil
		}
		for _, server := range lex.Matches(serverPattern) {
			for _, route := range lex.Matches(contextPattern) {
				if server[1] == route[1] && health == "" {
					health = route[2]
				}
			}
		}
		return nil
	})
	return health, err
}

func requiredEnv(name string) bool {
	switch name {
	case "DATABASE_URL", "REDIS_URL", "MONGO_URI", "MONGODB_URI", "MYSQL", "API_KEY", "TOKEN", "SECRET", "PASSWORD":
		return true
	}
	return strings.HasPrefix(name, "MYSQL_") || strings.HasSuffix(name, "_TOKEN") ||
		strings.HasSuffix(name, "_SECRET") || strings.HasSuffix(name, "_API_KEY") || strings.HasSuffix(name, "_PASSWORD")
}
