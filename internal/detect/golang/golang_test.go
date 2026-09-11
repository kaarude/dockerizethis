package golang_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/carl/dockerizethis/internal/detect/golang"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/stretchr/testify/require"
)

func TestFixtures(t *testing.T) {
	for _, tc := range []struct {
		name string
		want plan.Plan
	}{
		{"go-http", plan.Plan{Stack: "go", Version: "1.23", PkgManager: "go", Framework: "net/http", Process: plan.ProcessWeb, Port: 8080, HealthPath: "/health", Env: []plan.EnvVar{{Name: "PORT"}}, BuildCmd: "go build -o /app/server .", StartCmd: "go-http", Workdir: "/app", Confidence: 0.9}},
		{"go-worker", plan.Plan{Stack: "go", Version: "1.23", PkgManager: "go", Process: plan.ProcessWorker, Env: []plan.EnvVar{{Name: "QUEUE_NAME"}}, BuildCmd: "go build -o /app/server .", StartCmd: "go-worker", Workdir: "/app", Confidence: 0.8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ok, err := (golang.Detector{}).Detect(filepath.Join("..", "..", "..", "testdata", "fixtures", tc.name))
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, p)
		})
	}
	require.Equal(t, "go", (golang.Detector{}).Name())
}

func TestImportsEnvironmentAndEntryPackage(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/team/service\n\ngo 1.24.3\n")
	write(t, dir, "cmd/service/main.go", `package main
import (
 env "os"
 "github.com/gin-gonic/gin"
 _ "github.com/lib/pq"
 _ "github.com/jackc/pgx/v5"
 _ "github.com/redis/go-redis/v9"
 _ "github.com/go-sql-driver/mysql"
 _ "go.mongodb.org/mongo-driver/v2/mongo"
)
func main() {
 addr := env.Getenv("ADDR")
 if addr == "" { addr = "0.0.0.0:9090" }
 _ = env.Getenv("DATABASE_URL")
 _ = env.Getenv("DATABASE_URL")
 _ = env.Getenv("API_TOKEN")
 gin.Default().Run(addr)
}`)
	for _, name := range []string{"vendor/ignored.go", "testdata/ignored.go", "cmd/service/main_test.go"} {
		write(t, dir, name, "invalid ignored source")
	}
	write(t, dir, "nested/go.mod", "module nested")
	write(t, dir, "nested/main.go", "invalid nested source")
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "missing.go"), filepath.Join(dir, "link.go")))
	p, ok, err := (golang.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, plan.Plan{Stack: "go", Version: "1.24.3", PkgManager: "go", Framework: "gin", Process: plan.ProcessWeb, Port: 9090,
		Services: []plan.Service{plan.ServiceMongo, plan.ServiceMySQL, plan.ServicePostgres, plan.ServiceRedis},
		Env:      []plan.EnvVar{{Name: "ADDR"}, {Name: "API_TOKEN", Required: true}, {Name: "DATABASE_URL", Required: true}},
		BuildCmd: "go build -o /app/server ./cmd/service", StartCmd: "service", Workdir: "/app", Confidence: 0.9, Extras: map[string]string{"buildPackage": "./cmd/service"}}, p)
}

func TestServerEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, source, framework string
		process                 plan.ProcessType
	}{
		{"echo", `package main; import "github.com/labstack/echo/v4"; func main(){ echo.New().Start(":8080") }`, "echo", plan.ProcessWeb},
		{"chi", `package main; import "github.com/go-chi/chi/v5"; func main(){ _ = chi.NewRouter() }`, "chi", plan.ProcessWeb},
		{"server receiver", `package main; import h "net/http"; func main(){ s := &h.Server{Addr: ":8080"}; s.ListenAndServe() }`, "net/http", plan.ProcessWeb},
		{"declared server", `package main; import "net/http"; func main(){ var srv http.Server; srv.ListenAndServe() }`, "net/http", plan.ProcessWeb},
		{"https", `package main; import "net/http"; func main(){ http.ListenAndServeTLS(":8443", "cert", "key", nil) }`, "net/http", plan.ProcessWeb},
		{"client", `package main; import "net/http"; func main(){ http.Get("https://example.com") }`, "", plan.ProcessWorker},
		{"comment", `package main; func main() {} // http.ListenAndServe(":8080", nil)`, "", plan.ProcessWorker},
		{"non main", `package library; import "net/http"; func Serve(){ http.ListenAndServe(":8080", nil) }`, "", plan.ProcessWorker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "go.mod", "module example.com/test\ngo 1.23\n")
			write(t, dir, "main.go", tc.source)
			p, ok, err := (golang.Detector{}).Detect(dir)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.framework, p.Framework)
			require.Equal(t, tc.process, p.Process)
		})
	}
}

func TestCGOAndAmbiguousMain(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/cgo\ngo 1.23\n")
	write(t, dir, "main.go", "package main\nimport \"C\"\nfunc main() {}\n")
	p, ok, err := (golang.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "1", p.Extras["cgo"])
	require.NotEmpty(t, p.Notes)
	write(t, dir, "cmd/second/main.go", "package main; func main() {}")
	p, _, err = (golang.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.Less(t, p.Confidence, 0.8)
	require.Empty(t, p.BuildCmd)
	require.Contains(t, p.Notes[len(p.Notes)-1], "single main package")
}

func TestAbsentAndFailures(t *testing.T) {
	dir := t.TempDir()
	p, ok, err := (golang.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, plan.Plan{}, p)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "go.mod"), 0o755))
	_, ok, err = (golang.Detector{}).Detect(dir)
	require.ErrorContains(t, err, "read go.mod")
	require.False(t, ok)
	dir = t.TempDir()
	write(t, dir, "go.mod", "go 1.23\n")
	_, ok, err = (golang.Detector{}).Detect(dir)
	require.ErrorContains(t, err, "module directive")
	require.False(t, ok)
	write(t, dir, "go.mod", "module example.com/test\ngo 1.23\n")
	write(t, dir, "main.go", "invalid")
	_, ok, err = (golang.Detector{}).Detect(dir)
	require.ErrorContains(t, err, "scan Go sources")
	require.False(t, ok)
}

func write(t *testing.T, dir, name, data string) {
	t.Helper()
	filename := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	require.NoError(t, os.WriteFile(filename, []byte(data), 0o644))
}
