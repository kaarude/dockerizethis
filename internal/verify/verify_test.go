package verify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carl/dockerizethis/internal/plan"
	"github.com/stretchr/testify/require"
)

// Fake the Docker executable while exercising actual subprocesses and HTTP requests.
func fakeDocker(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_TEST_CALLS"
case "$1" in
 info) [ "$DOCKER_TEST_FAIL" != unavailable ] ;;
 build) echo 'fixture build output'; [ "$DOCKER_TEST_FAIL" != build ] ;;
 run) [ "$DOCKER_TEST_FAIL" != run ] ;;
 port) echo "$DOCKER_TEST_ADDRESS" ;;
 inspect) if [ "$DOCKER_TEST_FAIL" = stopped ]; then echo false; else echo true; fi ;;
 rm) [ "$DOCKER_TEST_FAIL" != cleanup ] ;;
 *) exit 1 ;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755))
	t.Setenv("PATH", dir)
	t.Setenv("DOCKER_TEST_CALLS", filepath.Join(dir, "calls"))
	t.Setenv("DOCKER_TEST_FAIL", "")
	return filepath.Join(dir, "calls")
}

func calls(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func TestBuildAndCodes(t *testing.T) {
	log := fakeDocker(t)
	for _, tc := range []struct {
		fail string
		code int
	}{{"", 0}, {"build", 2}, {"unavailable", 4}} {
		t.Setenv("DOCKER_TEST_FAIL", tc.fail)
		result, err := build(context.Background(), t.TempDir(), "custom/Dockerfile")
		require.Equal(t, tc.code, Code(err))
		if err != nil {
			require.Equal(t, tc.code, Code(fmt.Errorf("wrapped: %w", err)))
		}
		if tc.code == 0 {
			require.True(t, strings.HasPrefix(result.Image, "dockerizethis-verify:"))
			require.Contains(t, result.Output, "fixture build output")
		} else {
			require.Empty(t, result.Image)
		}
	}
	require.Contains(t, calls(t, log), "--file custom/Dockerfile .")
	require.Equal(t, 1, Code(errors.New("ordinary error")))
	t.Setenv("PATH", t.TempDir())
	_, err := build(context.Background(), t.TempDir(), "Dockerfile")
	require.Equal(t, 4, Code(err))
}

func TestSmokeHTTPAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name, path, fail string
		status, code     int
	}{
		{"healthy", "/health", "", 200, 0},
		{"root 404 proves startup", "", "", 404, 0},
		{"health 404 rejected before exit", "/health", "stopped", 404, 3},
		{"health 500 rejected before exit", "/health", "stopped", 500, 3},
		{"start failure", "/health", "run", 200, 3},
		{"stopped", "/health", "stopped", 503, 3},
		{"cleanup failure", "/health", "cleanup", 200, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := fakeDocker(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) }))
			defer server.Close()
			t.Setenv("DOCKER_TEST_ADDRESS", strings.TrimPrefix(server.URL, "http://"))
			t.Setenv("DOCKER_TEST_FAIL", tc.fail)
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=do-not-log-this\n"), 0o600))
			p := plan.Plan{Process: plan.ProcessWeb, Port: 8080, HealthPath: tc.path}
			timeout := 10 * time.Second
			if tc.fail == "" && tc.code == 3 {
				timeout = 3 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			result, err := smoke(ctx, dir, p, "dockerizethis-verify:test")
			require.Equal(t, tc.code, Code(err), "%v", err)
			if tc.fail != "run" {
				require.Equal(t, tc.status, result.StatusCode)
			}
			commands := calls(t, log)
			require.Contains(t, commands, "--publish 127.0.0.1::8080 --env-file .env")
			require.Contains(t, commands, "rm --force dockerizethis-smoke-")
			require.NotContains(t, commands, "do-not-log-this")
			require.NotContains(t, commands, "build")
		})
	}
}

func TestSmokeRejectsInvalidPlanWithoutDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, p := range []plan.Plan{
		{Process: plan.ProcessWorker},
		{Process: plan.ProcessWeb},
		{Process: plan.ProcessWeb, Port: 8080, HealthPath: "//outside/"},
		{Process: plan.ProcessWeb, Port: 8080, HealthPath: "/%zz"},
	} {
		_, err := smoke(context.Background(), t.TempDir(), p, "test")
		require.Equal(t, 3, Code(err))
	}
}

func TestSmokeDoesNotFollowRedirects(t *testing.T) {
	fakeDocker(t)
	externalCalls := 0
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { externalCalls++ }))
	defer external.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, external.URL, http.StatusFound) }))
	defer server.Close()
	t.Setenv("DOCKER_TEST_ADDRESS", strings.TrimPrefix(server.URL, "http://"))
	_, err := smoke(context.Background(), t.TempDir(), plan.Plan{Process: plan.ProcessWeb, Port: 8080, HealthPath: "/health"}, "test")
	require.NoError(t, err)
	require.Zero(t, externalCalls)
}
