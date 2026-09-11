package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/stretchr/testify/require"
)

func TestHelp(t *testing.T) {
	cmd := newRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())
	require.Contains(t, output.String(), "dockerizethis [path]")
	for _, flag := range []string{"dry-run", "yes", "json", "verify", "stack", "service", "force", "backup"} {
		require.Contains(t, output.String(), "--"+flag)
	}
	require.Contains(t, output.String(), `(default "build")`)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// execute runs the CLI with a JSON report and returns it decoded.
func execute(t *testing.T, args ...string) (report, error) {
	t.Helper()
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append(append([]string{}, args...), "--json"))
	err := cmd.Execute()
	var rep report
	if stdout.Len() > 0 {
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &rep), "stdout must stay valid JSON")
	}
	return rep, err
}

func goProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\ngo 1.24\n")
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	return dir
}

func TestRunWritesArtifacts(t *testing.T) {
	dir := goProject(t)
	rep, err := execute(t, dir, "--yes", "--verify=none")
	require.NoError(t, err)
	require.Equal(t, "go", rep.Plan.Stack)
	require.Equal(t, plan.ProcessWorker, rep.Plan.Process)
	require.NotEmpty(t, rep.Files)
	actions := map[string]string{}
	for _, r := range rep.Files {
		actions[r.Path] = r.Action
		require.FileExists(t, filepath.Join(dir, r.Path))
	}
	for _, want := range []string{"Dockerfile", ".dockerignore", "docker-compose.yml", ".github/workflows/docker.yml", "DEPLOY.md"} {
		require.Equal(t, "created", actions[want], want)
	}
	_, hasEnvExample := actions[".env.example"]
	require.False(t, hasEnvExample, "no env vars detected, so no .env.example")
	require.Nil(t, rep.Verify, "--verify=none skips verification")
}

func TestRunEmitsEnvExample(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"dependencies":{"express":"*"},"scripts":{"start":"node index.js"}}`)
	writeFile(t, dir, "index.js", "process.env.DATABASE_URL; app.listen(process.env.PORT || 8080)")
	rep, err := execute(t, dir, "--yes", "--verify=none")
	require.NoError(t, err)
	require.Equal(t, "node", rep.Plan.Stack)
	require.Equal(t, plan.ProcessWeb, rep.Plan.Process)
	require.Equal(t, 8080, rep.Plan.Port)
	content, err := os.ReadFile(filepath.Join(dir, ".env.example"))
	require.NoError(t, err)
	require.Contains(t, string(content), "DATABASE_URL=\n")
}

func TestRunDryRunWritesNothing(t *testing.T) {
	dir := goProject(t)
	prev := emit.SetDiffWriter(io.Discard)
	defer emit.SetDiffWriter(prev)
	rep, err := execute(t, dir, "--dry-run")
	require.NoError(t, err)
	require.NotEmpty(t, rep.Files)
	for _, r := range rep.Files {
		require.Equal(t, "would-create", r.Action)
		require.NoFileExists(t, filepath.Join(dir, r.Path))
	}
}

func TestRunExistingFiles(t *testing.T) {
	dir := goProject(t)
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")
	rep, err := execute(t, dir, "--yes", "--verify=none")
	require.NoError(t, err)
	for _, r := range rep.Files {
		if r.Path == "Dockerfile" {
			require.Equal(t, "skipped-exists", r.Action)
		}
	}
	content, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, "FROM scratch\n", string(content))

	rep, err = execute(t, dir, "--yes", "--verify=none", "--backup")
	require.NoError(t, err)
	for _, r := range rep.Files {
		if r.Path == "Dockerfile" {
			require.Equal(t, "backed-up", r.Action)
		}
	}
	require.FileExists(t, filepath.Join(dir, "Dockerfile.bak"))
	require.NotEqual(t, "FROM scratch\n", readFile(t, filepath.Join(dir, "Dockerfile")))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func TestRunDetectionFailures(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		_, err := execute(t, t.TempDir(), "--yes", "--verify=none")
		require.ErrorContains(t, err, "no supported stack detected")
	})
	t.Run("stack override mismatch", func(t *testing.T) {
		_, err := execute(t, goProject(t), "--yes", "--verify=none", "--stack=python")
		require.ErrorContains(t, err, "no python project detected")
	})
	t.Run("unknown stack", func(t *testing.T) {
		_, err := execute(t, goProject(t), "--yes", "--verify=none", "--stack=rust")
		require.ErrorContains(t, err, `unknown stack "rust"`)
	})
	t.Run("service escape", func(t *testing.T) {
		_, err := execute(t, goProject(t), "--yes", "--verify=none", "--service=../elsewhere")
		require.ErrorContains(t, err, "--service")
	})
	t.Run("service subdirectory", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "README.md", "monorepo")
		writeFile(t, root, "services/api/go.mod", "module example.com/api\ngo 1.24\n")
		writeFile(t, root, "services/api/main.go", "package main\n\nfunc main() {}\n")
		rep, err := execute(t, root, "--yes", "--verify=none", "--service=services/api")
		require.NoError(t, err)
		require.Equal(t, "go", rep.Plan.Stack)
		require.FileExists(t, filepath.Join(root, "services", "api", "Dockerfile"))
	})
}

func TestConfirm(t *testing.T) {
	p := plan.Plan{Stack: "go", Version: "1.24", Process: plan.ProcessWorker, Confidence: 0.8}
	files := []emit.File{{Path: "Dockerfile"}, {Path: "docker-compose.yml"}}
	for _, tc := range []struct {
		name, input string
		want        bool
	}{
		{"yes", "y\n", true}, {"YES", "YES\n", true}, {"no", "n\n", false},
		{"empty", "\n", false}, {"eof", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newRootCommand()
			var stderr bytes.Buffer
			cmd.SetErr(&stderr)
			cmd.SetIn(strings.NewReader(tc.input))
			err := confirm(cmd, "dir", p, files)
			if tc.want {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Contains(t, stderr.String(), "Write these 2 files")
		})
	}
}

func TestInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--verify=invalid"}, {"--verify="}, {"one", "two"}, {"--unknown"},
	} {
		cmd := newRootCommand()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs(args)
		require.Error(t, cmd.Execute())
		require.Empty(t, output.String())
	}
}
