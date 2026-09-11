package emit

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/carl/dockerizethis/internal/plan"
	"github.com/stretchr/testify/require"
)

func TestEnvExample(t *testing.T) {
	p := plan.Plan{Env: []plan.EnvVar{
		{Name: "REDIS_URL", Required: true, Hint: "Redis connection string"},
		{Name: "DEBUG"},
		{Name: "DATABASE_URL", Required: true, Hint: "Postgres connection string"},
		{Name: "SECRET_KEY"},
	}}

	f := EnvExample(p)

	require.Equal(t, ".env.example", f.Path)
	require.Equal(t, fs.FileMode(0o644), f.Mode)
	require.Equal(t, strings.Join([]string{
		"# Postgres connection string",
		"# required",
		"DATABASE_URL=",
		"DEBUG=",
		"# Redis connection string",
		"# required",
		"REDIS_URL=",
		"SECRET_KEY=",
		"",
	}, "\n"), string(f.Content))
}

func TestEnvExampleLeavesInputOrderAlone(t *testing.T) {
	env := []plan.EnvVar{{Name: "B"}, {Name: "A"}}
	p := plan.Plan{Env: env}

	EnvExample(p)

	require.Equal(t, []plan.EnvVar{{Name: "B"}, {Name: "A"}}, env)
}

func TestEnvExampleEmpty(t *testing.T) {
	f := EnvExample(plan.Plan{})
	require.Empty(t, f.Content)
}
