package java_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	detector "github.com/carl/dockerizethis/internal/detect/java"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/templates/java"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update Java template golden files")

func webPlan() plan.Plan {
	return plan.Plan{Stack: "java", Version: "21", PkgManager: "maven", Framework: "spring-boot", Process: plan.ProcessWeb, Port: 8080, BuildCmd: "mvn -B -DskipTests package", StartCmd: "java -jar /app/app.jar", Workdir: "/app"}
}

func TestRenderJavaGolden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*plan.Plan)
	}{
		{"maven-web", func(p *plan.Plan) {}},
		{"maven-worker", func(p *plan.Plan) { p.Process, p.Port = plan.ProcessWorker, 0 }},
		{"gradle", func(p *plan.Plan) {
			p.PkgManager, p.Framework = "gradle", "ktor"
			p.BuildCmd, p.StartCmd = "./gradlew --no-daemon build -x test", "java -cp /app/app.jar com.example.MainKt"
		}},
		{"quarkus", func(p *plan.Plan) { p.Framework = "quarkus" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := java.RenderJava(p)
			require.NoError(t, err)
			var output bytes.Buffer
			for _, file := range files {
				fmt.Fprintf(&output, "--- %s (mode %04o) ---\n%s", file.Path, file.Mode, file.Content)
			}
			golden := filepath.Join("..", "..", "..", "testdata", "golden", "java-"+tc.name+".golden")
			if *update {
				require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
				require.NoError(t, os.WriteFile(golden, output.Bytes(), 0o644))
			}
			want, err := os.ReadFile(golden)
			require.NoError(t, err)
			require.Equal(t, string(want), output.String(), "refresh with go test ./internal/templates/java -update")
		})
	}
}

func TestFixtureArtifacts(t *testing.T) {
	p, ok, err := (detector.Detector{}).Detect(filepath.Join("..", "..", "..", "testdata", "fixtures", "java-web"))
	require.NoError(t, err)
	require.True(t, ok)
	files, err := java.RenderJava(p)
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, "Dockerfile", files[0].Path)
	require.Equal(t, ".dockerignore", files[1].Path)
	dockerfile := string(files[0].Content)
	require.Contains(t, dockerfile, "FROM maven:3.9-eclipse-temurin-17 AS builder")
	require.Contains(t, dockerfile, "RUN mvn -B -DskipTests package\n")
	require.Contains(t, dockerfile, "cp target/*.jar /app/app.jar")
	require.Contains(t, dockerfile, "USER 10001:10001\n")
	require.True(t, strings.Contains(dockerfile, "EXPOSE 8080"))
	for _, file := range files {
		require.EqualValues(t, 0o644, file.Mode)
	}
}

func TestRenderJavaInvalidPlan(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*plan.Plan)
	}{
		{"stack", "stack", func(p *plan.Plan) { p.Stack = "node" }},
		{"version", "version", func(p *plan.Plan) { p.Version = "21\nRUN bad" }},
		{"missing version", "version", func(p *plan.Plan) { p.Version = "" }},
		{"process", "process", func(p *plan.Plan) { p.Process = plan.ProcessStatic }},
		{"port", "port", func(p *plan.Plan) { p.Port = 0 }},
		{"large port", "port", func(p *plan.Plan) { p.Port = 70000 }},
		{"pkg manager", "package manager", func(p *plan.Plan) { p.PkgManager = "ant" }},
		{"empty start", "start command", func(p *plan.Plan) { p.StartCmd = "" }},
		{"multiline start", "start command", func(p *plan.Plan) { p.StartCmd = "java -jar app.jar\nrm -rf /" }},
		{"multiline build", "build command", func(p *plan.Plan) { p.BuildCmd = "mvn package\nrm -rf /" }},
		{"trailing backslash", "build command", func(p *plan.Plan) { p.BuildCmd = "mvn package \\" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := java.RenderJava(p)
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, files)
		})
	}
}
