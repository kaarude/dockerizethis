package python_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	detector "github.com/carl/dockerizethis/internal/detect/python"
	"github.com/carl/dockerizethis/internal/plan"
	renderer "github.com/carl/dockerizethis/internal/templates/python"
	"github.com/stretchr/testify/require"
)

// Run with DOCKERIZETHIS_DOCKER_TEST=1 go test ./internal/templates/python -run TestDocker -v.
func TestDockerFixtures(t *testing.T) {
	if os.Getenv("DOCKERIZETHIS_DOCKER_TEST") != "1" {
		t.Skip("set DOCKERIZETHIS_DOCKER_TEST=1 to build and run generated images")
	}
	for _, fixture := range []string{"py-fastapi-pg", "py-django", "py-discord-worker"} {
		t.Run(fixture, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "..", "testdata", "fixtures", fixture))))
			verifyImage(t, dir, "worker started in offline mode")
		})
	}
}

// Generate real lockfiles with the corresponding manager, then build the emitted Dockerfile.
func TestDockerManagers(t *testing.T) {
	if os.Getenv("DOCKERIZETHIS_DOCKER_TEST") != "1" {
		t.Skip("set DOCKERIZETHIS_DOCKER_TEST=1 to build and run generated images")
	}
	for _, manager := range []string{"pip", "poetry", "uv", "pdm"} {
		t.Run(manager, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(`[build-system]
requires = ["setuptools>=68"]
build-backend = "setuptools.build_meta"

[project]
name = "fixture-worker"
version = "0.1.0"
requires-python = ">=3.12,<3.13"
dependencies = ["packaging==25.0"]

[tool.setuptools.packages.find]
where = ["src"]
`), 0o644))
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "src", "fixture_worker"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "fixture_worker", "__init__.py"), nil, 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "fixture_worker", "__main__.py"), []byte("import time\nfrom packaging.version import Version\nprint('worker ready ' + str(Version('1.0')), flush=True)\nwhile True:\n    time.sleep(1)\n"), 0o644))
			if manager != "pip" {
				docker(t, "run", "--rm", "--volume", dir+":/app", "--workdir", "/app", "python:3.12-slim", "sh", "-c", "pip install --quiet "+manager+" && "+manager+" lock")
			}
			verifyImage(t, dir, "worker ready 1.0")
		})
	}
}

func verifyImage(t *testing.T, dir, workerMessage string) {
	t.Helper()
	p, ok, err := (detector.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	files, err := renderer.RenderPython(p)
	require.NoError(t, err)
	for _, file := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, file.Path), file.Content, file.Mode))
	}
	image := fmt.Sprintf("dockerizethis-python-test-%d", time.Now().UnixNano())
	docker(t, "build", "--quiet", "--tag", image, dir)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "image", "rm", image).Run()
	})
	args := []string{"run", "--detach", "--env", "DJANGO_SECRET_KEY=local-fixture-verification"}
	if p.Process == plan.ProcessWeb {
		args = append(args, "--publish", fmt.Sprintf("127.0.0.1::%d", p.Port))
	}
	container := strings.TrimSpace(docker(t, append(args, image)...))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "rm", "--force", container).Run()
	})
	require.Equal(t, "10001:10001", strings.TrimSpace(docker(t, "inspect", "--format", "{{.Config.User}}", container)))
	if p.Process == plan.ProcessWeb {
		address := strings.TrimSpace(docker(t, "port", container, fmt.Sprintf("%d/tcp", p.Port)))
		client := http.Client{Timeout: time.Second}
		require.Eventually(t, func() bool {
			response, err := client.Get("http://" + address + "/health")
			if err != nil {
				return false
			}
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			return err == nil && response.StatusCode == 200 && strings.Contains(string(body), "ok")
		}, 30*time.Second, 200*time.Millisecond)
		require.Eventually(t, func() bool {
			return strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.Health.Status}}", container)) == "healthy"
		}, 40*time.Second, time.Second)
	} else {
		require.Eventually(t, func() bool { return strings.Contains(docker(t, "logs", container), workerMessage) }, 15*time.Second, 200*time.Millisecond)
	}
	require.Equal(t, "true", strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.Running}}", container)))
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	require.NoError(t, err, "docker %v\n%s", args, output)
	return string(output)
}

func TestDockerNativeDrivers(t *testing.T) {
	if os.Getenv("DOCKERIZETHIS_DOCKER_TEST") != "1" {
		t.Skip("set DOCKERIZETHIS_DOCKER_TEST=1 to build and run generated images")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("psycopg==3.2.9\nmysqlclient==2.2.7\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "worker.py"), []byte("import time\nimport psycopg\nimport MySQLdb\nprint('native drivers loaded', flush=True)\nwhile True:\n    time.sleep(1)\n"), 0o644))
	verifyImage(t, dir, "native drivers loaded")
}
