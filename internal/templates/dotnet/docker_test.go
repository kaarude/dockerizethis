package dotnet_test

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

	detector "github.com/carl/dockerizethis/internal/detect/dotnet"
	"github.com/carl/dockerizethis/internal/plan"
	renderer "github.com/carl/dockerizethis/internal/templates/dotnet"
	"github.com/stretchr/testify/require"
)

// Run with DOCKERIZETHIS_DOCKER_TEST=1 go test ./internal/templates/dotnet -run TestDocker -v.
func TestDocker(t *testing.T) {
	if os.Getenv("DOCKERIZETHIS_DOCKER_TEST") != "1" {
		t.Skip("set DOCKERIZETHIS_DOCKER_TEST=1 to build and run generated images")
	}
	for _, fixture := range []string{"dotnet-web", "dotnet-web-launch-port", "dotnet-worker"} {
		t.Run(fixture, func(t *testing.T) {
			dir := t.TempDir()
			fixtureDir := fixture
			if fixture == "dotnet-web-launch-port" {
				fixtureDir = "dotnet-web"
			}
			require.NoError(t, os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "..", "testdata", "fixtures", fixtureDir))))
			if fixture == "dotnet-web-launch-port" {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "Properties"), 0755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "Properties", "launchSettings.json"), []byte(`{"profiles":{"http":{"applicationUrl":"http://localhost:5217"}}}`), 0644))
			}
			p, ok, err := (detector.Detector{}).Detect(dir)
			require.NoError(t, err)
			require.True(t, ok)
			if fixture == "dotnet-web-launch-port" {
				require.Equal(t, 5217, p.Port)
			}
			files, err := renderer.RenderDotnet(p)
			require.NoError(t, err)
			for _, file := range files {
				require.NoError(t, os.WriteFile(filepath.Join(dir, file.Path), file.Content, file.Mode))
			}
			image := fmt.Sprintf("dockerizethis-dotnet-test-%d", time.Now().UnixNano())
			docker(t, "build", "--quiet", "--tag", image, dir)
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				_ = exec.CommandContext(cleanup, "docker", "image", "rm", image).Run()
			})
			args := []string{"run", "--detach"}
			if p.Process == plan.ProcessWeb {
				args = append(args, "--publish", fmt.Sprintf("127.0.0.1::%d", p.Port))
			}
			for _, v := range p.Env {
				if v.Required {
					args = append(args, "--env", v.Name+"=test")
				}
			}
			container := strings.TrimSpace(docker(t, append(args, image)...))
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				_ = exec.CommandContext(cleanup, "docker", "rm", "--force", container).Run()
			})
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
					return err == nil && response.StatusCode == 200 && string(body) == "ok"
				}, 30*time.Second, 200*time.Millisecond)
			} else {
				require.Eventually(t, func() bool { return strings.Contains(docker(t, "logs", container), "tick") }, 10*time.Second, 200*time.Millisecond)
			}
			require.Equal(t, "true", strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.Running}}", container)))
		})
	}
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	require.NoError(t, err, "docker %v\n%s", args, output)
	return string(output)
}
