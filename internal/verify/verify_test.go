package verify_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/carl/dockerizethis/internal/verify"
	"github.com/stretchr/testify/require"
)

func TestCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"pass", nil, verify.ExitOK},
		{"generic", errors.New("boom"), verify.ExitError},
		{"build", verify.ErrBuildFailed, verify.ExitBuildFailed},
		{"build wrapped", fmt.Errorf("image x: %w", verify.ErrBuildFailed), verify.ExitBuildFailed},
		{"smoke", verify.ErrSmokeFailed, verify.ExitSmokeFailed},
		{"smoke wrapped", fmt.Errorf("health: %w", verify.ErrSmokeFailed), verify.ExitSmokeFailed},
		{"docker", verify.ErrDockerUnavailable, verify.ExitDockerUnavailable},
		{"docker wrapped", fmt.Errorf("cli: %w", verify.ErrDockerUnavailable), verify.ExitDockerUnavailable},
		{"context canceled", context.Canceled, verify.ExitError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, verify.Code(tc.err))
		})
	}
}

// Without a docker binary on PATH, Build must fail with the friendly
// ErrDockerUnavailable so the CLI exits 4.
func TestBuildDockerUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	res, err := verify.Build(t.Context(), t.TempDir(), "Dockerfile")
	require.ErrorIs(t, err, verify.ErrDockerUnavailable)
	require.Equal(t, verify.ExitDockerUnavailable, verify.Code(err))
	require.False(t, res.OK)
}
