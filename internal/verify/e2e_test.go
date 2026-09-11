package verify_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/verify"
	"github.com/stretchr/testify/require"
)

// Real-docker tests run only under DOCKER_E2E=1 and still skip when no
// daemon answers, so the suite is safe on machines without Docker.
func requireDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("DOCKER_E2E") != "1" {
		t.Skip("set DOCKER_E2E=1 to run docker-dependent tests")
	}
	if _, err := verify.Build(t.Context(), t.TempDir(), "Dockerfile"); err != nil &&
		verify.Code(err) == verify.ExitDockerUnavailable {
		t.Skip("docker unavailable:", err)
	}
}

func writeDockerfile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(content), 0o644))
	return dir
}

func TestBuildE2ESuccess(t *testing.T) {
	requireDocker(t)
	dir := writeDockerfile(t, "FROM busybox\n")
	res, err := verify.Build(t.Context(), dir, "Dockerfile")
	require.NoError(t, err)
	require.True(t, res.OK)
	require.NotEmpty(t, res.Image)
}

func TestBuildE2EFailure(t *testing.T) {
	requireDocker(t)
	dir := writeDockerfile(t, "FROM busybox\nRUN exit 3\n")
	res, err := verify.Build(t.Context(), dir, "Dockerfile")
	require.ErrorIs(t, err, verify.ErrBuildFailed)
	require.Equal(t, verify.ExitBuildFailed, verify.Code(err))
	require.False(t, res.OK)
	require.Contains(t, res.FailedStep, "RUN exit 3")
	require.NotEmpty(t, res.Stderr)
}

func TestSmokeE2EWeb(t *testing.T) {
	requireDocker(t)
	dir := writeDockerfile(t, `FROM busybox
RUN mkdir -p /www && echo ok > /www/index.html
WORKDIR /www
EXPOSE 8080
CMD ["httpd", "-f", "-p", "8080"]
`)
	res, err := verify.Smoke(t.Context(), dir, plan.Plan{
		Process: plan.ProcessWeb, Port: 8080, HealthPath: "/",
	})
	require.NoError(t, err)
	require.True(t, res.OK)
	require.Equal(t, 200, res.StatusCode)
	require.NotZero(t, res.Attempts)
	if res.Build != nil {
		require.True(t, res.Build.OK)
	}
}

func TestSmokeE2EContainerDies(t *testing.T) {
	requireDocker(t)
	dir := writeDockerfile(t, "FROM busybox\nCMD [\"false\"]\n")
	res, err := verify.Smoke(t.Context(), dir, plan.Plan{
		Process: plan.ProcessWeb, Port: 8080, HealthPath: "/",
	})
	require.ErrorIs(t, err, verify.ErrSmokeFailed)
	require.Equal(t, verify.ExitSmokeFailed, verify.Code(err))
	require.False(t, res.OK)
	require.NotEmpty(t, res.Hint)
}
