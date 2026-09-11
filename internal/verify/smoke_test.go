package verify_test

import (
	"testing"

	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/verify"
	"github.com/stretchr/testify/require"
)

// Non-web plans have no HTTP endpoint, so Smoke skips rather than fails.
func TestSmokeSkipsNonWeb(t *testing.T) {
	for _, process := range []plan.ProcessType{plan.ProcessWorker, plan.ProcessStatic} {
		res, err := verify.Smoke(t.Context(), t.TempDir(), plan.Plan{
			Process: process, Port: 8080,
		})
		require.NoError(t, err)
		require.True(t, res.Skipped)
		require.False(t, res.OK)
		require.Contains(t, res.Reason, string(process))
	}
}

func TestSmokeRequiresPort(t *testing.T) {
	_, err := verify.Smoke(t.Context(), t.TempDir(), plan.Plan{Process: plan.ProcessWeb})
	require.Error(t, err)
	require.NotErrorIs(t, err, verify.ErrSmokeFailed)
	require.Equal(t, verify.ExitError, verify.Code(err))
}

func TestSmokeDockerUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	res, err := verify.Smoke(t.Context(), t.TempDir(), plan.Plan{
		Process: plan.ProcessWeb, Port: 8080, HealthPath: "/",
	})
	require.ErrorIs(t, err, verify.ErrDockerUnavailable)
	require.Equal(t, verify.ExitDockerUnavailable, verify.Code(err))
	require.False(t, res.OK)
}
