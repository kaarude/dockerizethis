package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Exercise the generated Compose file, including env_file resolution, rather
// than only building its Dockerfile. No project .env is created for startup.
func TestComposeOptionalEnvironment(t *testing.T) {
	if os.Getenv("DOCKERIZETHIS_DOCKER_TEST") != "1" {
		t.Skip("set DOCKERIZETHIS_DOCKER_TEST=1 to build and start generated Compose services")
	}
	dir := fixture(t, "go-http")
	_, _, err := execute(t, dir, "--yes", "--verify=none")
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(dir, ".env"))

	// Isolate the test from apps already listening on the fixture's host port.
	composePath := filepath.Join(dir, "docker-compose.yml")
	compose, err := os.ReadFile(composePath)
	require.NoError(t, err)
	require.Contains(t, string(compose), `"8080:8080"`)
	compose = []byte(strings.Replace(string(compose), `"8080:8080"`, `"127.0.0.1::8080"`, 1))
	require.NoError(t, os.WriteFile(composePath, compose, 0o644))
	project := fmt.Sprintf("dockerizethis-env-test-%d", time.Now().UnixNano())
	composeCommand := func(ctx context.Context, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "docker", append([]string{"compose", "--project-directory", dir, "--project-name", project}, args...)...)
	}
	runCompose := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
		defer cancel()
		output, err := composeCommand(ctx, args...).CombinedOutput()
		require.NoError(t, err, "docker compose %v\n%s", args, output)
		return output
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		output, err := composeCommand(ctx, "down", "--rmi", "local").CombinedOutput()
		if err != nil {
			t.Errorf("clean up test Compose project: %v\n%s", err, output)
		}
	})
	runCompose("up", "--detach", "--build")
	address := strings.TrimSpace(string(runCompose("port", "app", "8080")))
	client := http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		response, err := client.Get("http://" + address + "/health")
		if err != nil {
			return false
		}
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode == http.StatusOK
	}, 15*time.Second, 100*time.Millisecond)
	require.NoFileExists(t, filepath.Join(dir, ".env"))

	// An existing file must still supply overrides and survive regeneration.
	envPath := filepath.Join(dir, ".env")
	env := []byte("PORT=8123\n")
	require.NoError(t, os.WriteFile(envPath, env, 0o600))
	_, _, err = execute(t, dir, "--yes", "--verify=none", "--force")
	require.NoError(t, err)
	got, err := os.ReadFile(envPath)
	require.NoError(t, err)
	require.Equal(t, env, got)
	var config struct {
		Services map[string]struct {
			Environment map[string]string `json:"environment"`
		} `json:"services"`
	}
	require.NoError(t, json.Unmarshal(runCompose("config", "--format", "json"), &config))
	require.Equal(t, "8123", config.Services["app"].Environment["PORT"])
}
