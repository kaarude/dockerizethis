package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carl/dockerizethis/internal/detect"
	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/verify"
	"github.com/stretchr/testify/require"
)

func execute(t *testing.T, args ...string) (report, string, error) {
	t.Helper()
	cmd := newRootCommand()
	var output, diagnostics bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&diagnostics)
	cmd.SetArgs(append(args, "--json"))
	err := cmd.ExecuteContext(context.Background())
	var r report
	require.NoError(t, json.Unmarshal(output.Bytes(), &r), output.String())
	return r, diagnostics.String(), err
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS(filepath.Join("../../testdata/fixtures", name))))
	return dir
}

func TestHelp(t *testing.T) {
	cmd := newRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())
	for _, flag := range []string{"dry-run", "yes", "json", "verify", "stack", "service", "force", "backup"} {
		require.Contains(t, output.String(), "--"+flag)
	}
	require.Contains(t, output.String(), `(default "build")`)
	require.NotContains(t, output.String(), "scaffold")
}

func TestEveryFixture(t *testing.T) {
	entries, err := os.ReadDir("../../testdata/fixtures")
	require.NoError(t, err)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			dir := fixture(t, entry.Name())
			r, _, err := execute(t, dir, "--verify=none")
			require.NoError(t, err)
			require.NotNil(t, r.Plan)
			require.GreaterOrEqual(t, len(r.Results), 5)
			require.Equal(t, "skipped", r.Verify.Status)
			for _, file := range r.Results {
				require.Equal(t, "created", file.Action)
				content, err := os.ReadFile(filepath.Join(dir, file.Path))
				require.NoError(t, err)
				require.NotEmpty(t, content)
			}
			require.NoFileExists(t, filepath.Join(dir, ".env"))
			r, _, err = execute(t, dir, "--verify=none")
			require.NoError(t, err)
			for _, file := range r.Results {
				require.Equal(t, "skipped-exists", file.Action)
			}
		})
	}
}

func TestDryRunNeverWritesOrRunsDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, level := range []string{"none", "build", "full"} {
		dir := fixture(t, "node-express-pg")
		before := snapshot(t, dir)
		r, _, err := execute(t, dir, "--dry-run", "--verify="+level, "--force", "--backup")
		require.NoError(t, err)
		require.Equal(t, "dry-run", r.Verify.Reason)
		require.Nil(t, r.Verify.Build)
		require.Nil(t, r.Verify.Smoke)
		require.Equal(t, before, snapshot(t, dir))
		for _, result := range r.Results {
			require.Equal(t, "would-create", result.Action)
		}
	}
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[path] = "directory"
			return nil
		}
		content, err := os.ReadFile(path)
		files[path] = string(content)
		return err
	}))
	return files
}

func TestServiceAndDefaultPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "services", "api")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.CopyFS(dir, os.DirFS("../../testdata/fixtures/go-http")))
	r, _, err := execute(t, root, "--service=services/api", "--verify=none")
	require.NoError(t, err)
	require.Equal(t, "go", r.Plan.Stack)
	require.FileExists(t, filepath.Join(dir, "Dockerfile"))
	require.NoFileExists(t, filepath.Join(root, "Dockerfile"))
	t.Chdir(dir)
	r, _, err = execute(t, "--dry-run")
	require.NoError(t, err)
	require.Equal(t, "go", r.Plan.Stack)
}

func TestDetectionErrorsDoNotWrite(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(string)
		args  []string
		want  string
	}{
		{name: "empty", want: "package.json, go.mod, pyproject.toml, requirements.txt, setup.py"},
		{name: "wrong stack", setup: func(dir string) { require.NoError(t, os.CopyFS(dir, os.DirFS("../../testdata/fixtures/go-http"))) }, args: []string{"--stack=node"}, want: `stack "node" was not detected`},
		{name: "bad manifest", setup: func(dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte("{"), 0o644))
		}, want: "parse package.json"},
		{name: "escaping service", args: []string{"--service=../other"}, want: "subdirectory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.setup != nil {
				tc.setup(dir)
			}
			before := snapshot(t, dir)
			r, _, err := execute(t, append([]string{dir, "--verify=none"}, tc.args...)...)
			require.ErrorContains(t, err, tc.want)
			require.Empty(t, r.Results)
			require.Equal(t, before, snapshot(t, dir))
		})
	}
}

type detectorStub struct {
	name  string
	p     plan.Plan
	ok    bool
	err   error
	calls *int
}

func (d detectorStub) Name() string                           { return d.name }
func (d detectorStub) Detect(string) (plan.Plan, bool, error) { *d.calls++; return d.p, d.ok, d.err }

func TestDetectorSelection(t *testing.T) {
	calls := 0
	detectors := []detect.Detector{
		detectorStub{"node", plan.Plan{Stack: "node", Confidence: 0.6}, true, nil, &calls},
		detectorStub{"go", plan.Plan{Stack: "go", Confidence: 0.9}, true, nil, &calls},
		detectorStub{"python", plan.Plan{Stack: "python", Confidence: 0.9}, true, nil, &calls},
	}
	p, err := detectPlan(".", "", detectors)
	require.NoError(t, err)
	require.Equal(t, "go", p.Stack)
	require.Equal(t, 3, calls)
	p, err = detectPlan(".", "node", detectors)
	require.NoError(t, err)
	require.Equal(t, "node", p.Stack)
	require.Equal(t, 6, calls)
	sentinel := errors.New("broken manifest")
	detectors[0] = detectorStub{"node", plan.Plan{}, false, sentinel, &calls}
	_, err = detectPlan(".", "", detectors)
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 9, calls)
}

func TestConfirmation(t *testing.T) {
	for _, tc := range []struct {
		answer   string
		accepted bool
	}{{"y\n", true}, {" YES \n", true}, {"n\n", false}, {"\n", false}, {"", false}, {"maybe\n", false}} {
		var out bytes.Buffer
		accepted, err := confirm(strings.NewReader(tc.answer), &out, []emit.File{{Path: "Dockerfile"}, {Path: ".env.example"}})
		require.NoError(t, err)
		require.Equal(t, tc.accepted, accepted)
		require.Contains(t, out.String(), "Dockerfile\n  .env.example\nWrite these files? [y/N]")
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	require.NoError(t, err)
	defer func() { _ = null.Close() }()
	require.False(t, isTerminal(null))
}

func TestOverwriteAndPartialFailure(t *testing.T) {
	for _, flag := range []string{"--force", "--backup"} {
		dir := fixture(t, "go-http")
		target := filepath.Join(dir, "Dockerfile")
		require.NoError(t, os.WriteFile(target, []byte("original"), 0o644))
		r, _, err := execute(t, dir, "--verify=none", flag)
		require.NoError(t, err)
		if flag == "--backup" {
			require.Equal(t, "backed-up", r.Results[0].Action)
			original, err := os.ReadFile(target + ".bak")
			require.NoError(t, err)
			require.Equal(t, "original", string(original))
		} else {
			require.Equal(t, "created", r.Results[0].Action)
		}
	}
	dir := fixture(t, "go-http")
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".dockerignore"), 0o755))
	r, _, err := execute(t, dir, "--verify=none")
	require.ErrorContains(t, err, "not a regular file")
	require.Equal(t, []emit.Result{{Path: "Dockerfile", Action: "created"}}, r.Results)
}

func TestComposeEnvironmentAndHumanReport(t *testing.T) {
	p := completePlan(plan.Plan{Stack: "go", Process: plan.ProcessWeb, Services: []plan.Service{plan.ServicePostgres, plan.ServiceMySQL, plan.ServiceMongo}, Env: []plan.EnvVar{{Name: "MYSQL_PASSWORD"}}})
	require.Len(t, p.Env, 4)
	for _, v := range p.Env {
		require.True(t, v.Required)
		require.NotEmpty(t, v.Hint)
	}
	dir := fixture(t, "node-express-pg")
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{dir, "--verify=none"})
	require.NoError(t, cmd.Execute())
	for _, want := range []string{"framework: express", "process: web", "Port: 8080", "services: postgres", "confidence: 100%", "created Dockerfile", "DATABASE_URL (required)", "POSTGRES_PASSWORD (required)", "docker compose up -d --build"} {
		require.Contains(t, out.String(), want)
	}
}

func TestUnavailableDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	r, _, err := execute(t, fixture(t, "go-http"), "--verify=full")
	require.Equal(t, 4, verify.Code(err))
	require.Equal(t, "failed", r.Verify.Status)
	require.NotEmpty(t, r.Results)
	require.Nil(t, r.Verify.Smoke)
}

func TestInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"--verify=invalid"}, {"--verify="}, {"one", "two"}, {"--unknown"}} {
		cmd := newRootCommand()
		cmd.SetOut(io.Discard)
		cmd.SetArgs(args)
		require.Error(t, cmd.Execute())
	}
}

func TestVerificationDispatch(t *testing.T) {
	for _, tc := range []struct {
		fixture, mode, fail string
		code                int
		build, smoke        bool
	}{
		{"go-http", "none", "", 0, false, false},
		{"go-http", "build", "", 0, true, false},
		{"go-http", "full", "build", 2, true, false},
		{"go-http", "full", "run", 3, true, true},
		{"go-worker", "full", "", 0, true, false},
	} {
		t.Run(tc.fixture+tc.mode+tc.fail, func(t *testing.T) {
			bin := t.TempDir()
			log := filepath.Join(bin, "calls")
			script := `#!/bin/sh
printf '%s\n' "$1" >> "$DOCKER_TEST_CALLS"
case "$1" in
info) exit 0;;
build) [ "$DOCKER_TEST_FAIL" != build ];;
run) exit 1;;
rm) exit 0;;
*) exit 1;;
esac
`
			require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755))
			t.Setenv("PATH", bin)
			t.Setenv("DOCKER_TEST_CALLS", log)
			t.Setenv("DOCKER_TEST_FAIL", tc.fail)
			r, _, err := execute(t, fixture(t, tc.fixture), "--verify="+tc.mode)
			require.Equal(t, tc.code, verify.Code(err), "%v", err)
			require.Equal(t, tc.build, r.Verify.Build != nil)
			require.Equal(t, tc.smoke, r.Verify.Smoke != nil)
			if !tc.build {
				require.NoFileExists(t, log)
			}
		})
	}
}

type brokenWriter struct{ err error }

func (w brokenWriter) Write([]byte) (int, error) { return 0, w.err }

func TestProgressFailureStopsBeforeDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	sentinel := errors.New("closed progress stream")
	cmd := newRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(brokenWriter{sentinel})
	cmd.SetArgs([]string{fixture(t, "go-http"), "--json"})
	err := cmd.Execute()
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 1, verify.Code(err))
	var r report
	require.NoError(t, json.Unmarshal(output.Bytes(), &r))
	require.Nil(t, r.Verify.Build)
}

func TestServiceSymlinkStaysWithinProject(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "internal", true: "external"}[external], func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "actual")
			if external {
				target = filepath.Join(t.TempDir(), "actual")
			}
			require.NoError(t, os.MkdirAll(target, 0755))
			require.NoError(t, os.CopyFS(target, os.DirFS("../../testdata/fixtures/go-worker")))
			require.NoError(t, os.Symlink(target, filepath.Join(root, "api")))
			before := snapshot(t, target)
			r, _, err := execute(t, root, "--service=api", "--verify=none", "--yes")
			if external {
				require.ErrorContains(t, err, "subdirectory within the project")
				require.Empty(t, r.Results)
				require.Equal(t, before, snapshot(t, target))
			} else {
				require.NoError(t, err)
				require.FileExists(t, filepath.Join(target, "Dockerfile"))
			}
		})
	}
}

type failOnSmokeProgress struct{}

func (failOnSmokeProgress) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "Checking HTTP startup") {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestSmokeProgressFailureDoesNotReportPass(t *testing.T) {
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\ncase \"$1\" in info|build) exit 0;; *) exit 1;; esac\n"), 0755))
	t.Setenv("PATH", bin)
	cmd := newRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(failOnSmokeProgress{})
	cmd.SetArgs([]string{fixture(t, "go-http"), "--json", "--verify=full"})
	require.ErrorIs(t, cmd.Execute(), io.ErrClosedPipe)
	var r report
	require.NoError(t, json.Unmarshal(output.Bytes(), &r))
	require.Equal(t, "failed", r.Verify.Status)
	require.Nil(t, r.Verify.Smoke)
}
