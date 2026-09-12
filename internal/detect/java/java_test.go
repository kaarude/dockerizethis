package java_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/carl/dockerizethis/internal/detect/java"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/stretchr/testify/require"
)

func TestFixture(t *testing.T) {
	p, ok, err := (java.Detector{}).Detect(filepath.Join("..", "..", "..", "testdata", "fixtures", "java-web"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, plan.Plan{Stack: "java", Version: "17", PkgManager: "maven", Framework: "jdk-httpserver", Process: plan.ProcessWeb, Port: 8080, HealthPath: "/health", Env: []plan.EnvVar{{Name: "PORT"}}, BuildCmd: "mvn -B -DskipTests package", StartCmd: "java -cp /app/app.jar example.Main", Workdir: "/app", Confidence: 0.9}, p)
	require.Equal(t, "java", (java.Detector{}).Name())
}

func TestSpringBootMaven(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "mvnw", "#!/bin/sh\n")
	write(t, dir, "pom.xml", `<project>
  <groupId>com.example</groupId><artifactId>shop</artifactId><version>1.0</version>
  <parent><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-parent</artifactId><version>3.4.0</version></parent>
  <properties><java.version>21</java.version></properties>
  <dependencies>
    <dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-web</artifactId></dependency>
    <dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-data-jpa</artifactId></dependency>
    <dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId></dependency>
    <dependency><groupId>redis.clients</groupId><artifactId>jedis</artifactId></dependency>
  </dependencies>
</project>`)
	write(t, dir, "src/main/resources/application.properties", "server.port=9090\n")
	write(t, dir, "src/main/java/com/example/shop/App.java", `package com.example.shop;
import org.springframework.boot.SpringApplication;
public class App {
    public static void main(String[] args) {
        String url = System.getenv("DATABASE_URL");
        SpringApplication.run(App.class, args);
    }
}`)
	p, ok, err := (java.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "maven", p.PkgManager)
	require.Equal(t, "./mvnw -B -DskipTests package", p.BuildCmd)
	require.Equal(t, "spring-boot", p.Framework)
	require.Equal(t, plan.ProcessWeb, p.Process)
	require.Equal(t, 9090, p.Port, "server.port from application.properties")
	require.Equal(t, "21", p.Version)
	require.Equal(t, "java -jar /app/app.jar", p.StartCmd)
	require.Equal(t, []plan.Service{plan.ServicePostgres, plan.ServiceRedis}, p.Services)
	require.Equal(t, []plan.EnvVar{{Name: "DATABASE_URL", Required: true}}, p.Env)
}

func TestGradleVariants(t *testing.T) {
	for _, tc := range []struct {
		name, buildFile, build, source string
		version                        string
		framework                      string
		process                        plan.ProcessType
		port                           int
		startCmd                       string
	}{
		{"ktor", "build.gradle.kts", `plugins { kotlin("jvm") version "2.0" }
dependencies { implementation("io.ktor:ktor-server-netty:3.0") }
kotlin { jvmToolchain(21) }`, `fun main() { embeddedServer(Netty, port = 8181).start() }`, "21", "ktor", plan.ProcessWeb, 8181, "java -cp /app/app.jar com.example.MainKt"},
		{"spring gradle worker", "build.gradle", `plugins { id 'org.springframework.boot' version '3.4.0' }
dependencies { implementation 'org.springframework.boot:spring-boot-starter' }
tasks.named('compileJava') { options.release = 17 }`, `package com.example; public class Job { public static void main(String[] a) {} }`, "21", "spring-boot", plan.ProcessWorker, 0, "java -jar /app/app.jar"},
		{"jda bot", "build.gradle", `plugins { id 'java' }
dependencies { implementation 'net.dv8tion:JDA:5.2' }
sourceCompatibility = 17`, `public class Bot { public static void main(String[] a) { System.getenv("DISCORD_TOKEN"); } }`, "17", "JDA", plan.ProcessWorker, 0, "java -cp /app/app.jar Bot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, tc.buildFile, tc.build)
			if tc.name == "ktor" {
				write(t, dir, "src/main/kotlin/com/example/Main.kt", "package com.example\n"+tc.source)
			} else {
				write(t, dir, "src/main/java/"+map[string]string{"spring gradle worker": "com/example/Job.java", "jda bot": "Bot.java"}[tc.name], tc.source)
			}
			p, ok, err := (java.Detector{}).Detect(dir)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, "gradle", p.PkgManager)
			require.Equal(t, tc.version, p.Version)
			require.Equal(t, tc.framework, p.Framework)
			require.Equal(t, tc.process, p.Process)
			require.Equal(t, tc.port, p.Port)
			require.Equal(t, tc.startCmd, p.StartCmd)
		})
	}
}

func TestBothBuildToolsAndNoMain(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pom.xml", "<project><groupId>x</groupId><artifactId>y</artifactId></project>")
	write(t, dir, "build.gradle", "plugins { id 'java' }\n")
	write(t, dir, "gradlew", "#!/bin/sh\n")
	p, ok, err := (java.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "gradle", p.PkgManager, "gradlew tips the balance to Gradle")
	require.Contains(t, p.Notes[0], "both Maven and Gradle")
	require.Equal(t, "./gradlew --no-daemon build -x test", p.BuildCmd)
	require.Equal(t, "java -jar /app/app.jar", p.StartCmd)
	require.Equal(t, 0.5, p.Confidence, "no main class found")
}

func TestAbsentAndFailures(t *testing.T) {
	dir := t.TempDir()
	p, ok, err := (java.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, plan.Plan{}, p)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "pom.xml"), 0o755))
	_, ok, err = (java.Detector{}).Detect(dir)
	require.ErrorContains(t, err, "read pom.xml")
	require.False(t, ok)
}

func write(t *testing.T, dir, name, data string) {
	t.Helper()
	filename := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	require.NoError(t, os.WriteFile(filename, []byte(data), 0o644))
}

func TestJPADoesNotChooseDatabase(t *testing.T) {
	for _, tc := range []struct {
		group, artifact string
		services        []plan.Service
	}{
		{"com.h2database", "h2", nil},
		{"com.mysql", "mysql-connector-j", []plan.Service{plan.ServiceMySQL}},
		{"org.postgresql", "postgresql", []plan.Service{plan.ServicePostgres}},
	} {
		dir := t.TempDir()
		write(t, dir, "pom.xml", `<project><dependencies><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-data-jpa</artifactId></dependency><dependency><groupId>`+tc.group+`</groupId><artifactId>`+tc.artifact+`</artifactId></dependency></dependencies></project>`)
		p, ok, err := (java.Detector{}).Detect(dir)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, tc.services, p.Services)
	}
}

func TestHealthRoutes(t *testing.T) {
	for _, tc := range []struct{ name, code, file, want string }{
		{"exact context", `server.createContext("/healthz", exchange -> {});`, "src/main/java/Main.java", "/healthz"},
		{"unrelated value", `String path = "/health";`, "src/main/java/Main.java", ""},
		{"comment", `// server.createContext("/health", exchange -> {});`, "src/main/java/Main.java", ""},
		{"block comment", `/* server.createContext("/health", exchange -> {}); */`, "src/main/java/Main.java", ""},
		{"string", `String example = "server.createContext(\"/health\", handler)";`, "src/main/java/Main.java", ""},
		{"text block", `String example = """server.createContext("/health", handler)""";`, "src/main/java/Main.java", ""},
		{"test", `server.createContext("/health", exchange -> {});`, "src/test/java/Main.java", ""},
		{"method restriction", `server.createContext("/health", exchange -> { if (exchange.getRequestMethod().equals("POST")) {} });`, "src/main/java/Main.java", ""},
		{"test annotation", `@Test void test() { server.createContext("/health", exchange -> {}); }`, "src/main/java/Main.java", ""},
		{"prefixed annotation", `@RequestMapping("/api") class Health { @GetMapping("/health") String health() { return "ok"; } }`, "src/main/java/Main.java", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "pom.xml", `<project/>`)
			write(t, dir, tc.file, "HttpServer server = HttpServer.create(new InetSocketAddress(8080), 0);\n"+tc.code)
			p, ok, err := (java.Detector{}).Detect(dir)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, p.HealthPath)
		})
	}
}

func TestMavenRuntimeEvidence(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pom.xml", `<project>
 <properties><maven.compiler.release>1.8</maven.compiler.release></properties>
 <!-- <dependencies><dependency><artifactId>spring-boot-starter-web</artifactId></dependency></dependencies> -->
 <dependencyManagement><dependencies><dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId></dependency></dependencies></dependencyManagement>
 <dependencies><dependency><groupId>com.mysql</groupId><artifactId>mysql-connector-j</artifactId><scope>test</scope></dependency></dependencies>
 </project>`)
	p, ok, err := (java.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, p.Services)
	require.Equal(t, plan.ProcessWorker, p.Process)
	require.Equal(t, "8", p.Version)
	write(t, dir, "pom.xml", "<project>")
	_, ok, err = (java.Detector{}).Detect(dir)
	require.ErrorContains(t, err, "parse pom.xml")
	require.False(t, ok)
}
