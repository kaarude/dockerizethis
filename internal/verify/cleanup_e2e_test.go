package verify_test

import (
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/verify"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunE2EFailedStartRemovesCreatedContainer(t *testing.T) {
	if os.Getenv("DOCKER_E2E") != "1" {
		t.Skip("set DOCKER_E2E=1")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\nCMD [\"/dockerizethis-review-missing\"]\n"), 0644))
	result, err := verify.Run(t.Context(), dir, plan.Plan{Process: plan.ProcessWeb, Port: 8080}, verify.Options{Mode: verify.ModeFull})
	require.Error(t, err)
	require.NotEmpty(t, result.Build.Image)
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", result.Build.Image).Run() })
	out, listErr := exec.Command("docker", "ps", "-aq", "--filter", "ancestor="+result.Build.Image).Output()
	require.NoError(t, listErr)
	ids := strings.Fields(string(out))
	for _, id := range ids {
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	}
	require.Empty(t, ids, "Smoke left a container behind after Docker created it but failed to start")
}
