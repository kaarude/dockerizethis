package verify

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/carl/dockerizethis/internal/plan"
	"github.com/stretchr/testify/require"
)

func TestRunOwnsImageHandoffAndCleanup(t *testing.T) {
	for _, fail := range []string{"", "run", "cleanup"} {
		t.Run("failure="+fail, func(t *testing.T) {
			log := fakeDocker(t)
			t.Setenv("DOCKER_TEST_FAIL", fail)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			defer server.Close()
			t.Setenv("DOCKER_TEST_ADDRESS", strings.TrimPrefix(server.URL, "http://"))
			p := plan.Plan{Process: plan.ProcessWeb, Port: 8080, HealthPath: "/health", Extras: map[string]string{"original": "kept"}}
			var progress bytes.Buffer
			report, err := Run(t.Context(), t.TempDir(), p, Options{Mode: ModeFull, Progress: &progress})
			if fail == "" {
				require.NoError(t, err)
				require.Equal(t, "passed", report.Status)
			} else {
				require.Equal(t, 3, Code(err))
				require.Equal(t, "failed", report.Status)
				require.NotEmpty(t, report.Error)
			}
			require.NotNil(t, report.Build)
			commands := calls(t, log)
			require.Equal(t, 1, strings.Count(commands, "build --load"))
			require.Contains(t, commands, "run --detach")
			require.Contains(t, commands, "127.0.0.1::8080 "+report.Build.Image)
			require.Contains(t, commands, "rm --force dockerizethis-smoke-")
			require.Equal(t, map[string]string{"original": "kept"}, p.Extras)
		})
	}
}

func TestRunSkipAndBuildModes(t *testing.T) {
	for _, tc := range []struct {
		mode    Mode
		process plan.ProcessType
		status  string
		build   bool
	}{
		{ModeNone, plan.ProcessWeb, "skipped", false},
		{ModeBuild, plan.ProcessWeb, "passed", true},
		{ModeFull, plan.ProcessWorker, "passed", true},
		{ModeFull, plan.ProcessStatic, "passed", true},
	} {
		t.Run(string(tc.mode)+"/"+string(tc.process), func(t *testing.T) {
			log := fakeDocker(t)
			report, err := Run(t.Context(), t.TempDir(), plan.Plan{Process: tc.process}, Options{Mode: tc.mode})
			require.NoError(t, err)
			require.Equal(t, tc.status, report.Status)
			require.Nil(t, report.Smoke)
			if tc.build {
				require.NotNil(t, report.Build)
				require.NotContains(t, calls(t, log), "run --detach")
			} else {
				require.NoFileExists(t, log)
			}
		})
	}
}

type brokenProgress struct{ onSmoke bool }

func (w brokenProgress) Write(p []byte) (int, error) {
	if !w.onSmoke || strings.Contains(string(p), "Checking HTTP") {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}
func TestRunStopsOnProgressFailure(t *testing.T) {
	for _, onSmoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "before build", true: "before smoke"}[onSmoke], func(t *testing.T) {
			log := fakeDocker(t)
			report, err := Run(t.Context(), t.TempDir(), plan.Plan{Process: plan.ProcessWeb}, Options{Mode: ModeFull, Progress: brokenProgress{onSmoke}})
			require.ErrorIs(t, err, io.ErrClosedPipe)
			require.Equal(t, "failed", report.Status)
			require.Nil(t, report.Smoke)
			if onSmoke {
				require.NotContains(t, calls(t, log), "run --detach")
			} else {
				require.NoFileExists(t, log)
			}
		})
	}
}

func TestRunCanceledBeforeBuild(t *testing.T) {
	log := fakeDocker(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	report, err := Run(ctx, t.TempDir(), plan.Plan{}, Options{Mode: ModeBuild})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, Code(err))
	require.Equal(t, "failed", report.Status)
	require.NoFileExists(t, log)
}

func TestRunBoundsLogsAndReportsBuildFailure(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	text := strings.Repeat("old build output line\n", 6000) + "#7 [4/5] RUN npm ci\n#7 ERROR: npm ERR! package-lock.json missing\n"
	require.NoError(t, os.WriteFile(log, []byte(text), 0600))
	script := `#!/bin/sh
case "$1" in
 info) exit 0;;
 build) /bin/cat "$DOCKER_BUILD_LOG"; exit 1;;
 *) exit 1;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755))
	t.Setenv("PATH", bin)
	t.Setenv("DOCKER_BUILD_LOG", log)
	report, err := Run(t.Context(), t.TempDir(), plan.Plan{}, Options{Mode: ModeBuild})
	require.Equal(t, 2, Code(err))
	require.Equal(t, "failed", report.Status)
	require.LessOrEqual(t, len(report.Build.Output), 64<<10)
	require.Contains(t, report.Build.Output, "npm ERR!")
	require.Equal(t, "[4/5] RUN npm ci", report.Build.FailedStep)
	require.Contains(t, report.Build.Hint, "package-lock.json")
}

func TestRunCancellationBoundsInheritedOutputPipes(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "child")
	script := `#!/bin/sh
case "$1" in
 info) exit 0;;
 build) /bin/sleep 5 & echo $! > "$DOCKER_CHILD_PID"; wait;;
 *) exit 1;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755))
	t.Setenv("PATH", bin)
	t.Setenv("DOCKER_CHILD_PID", marker)
	t.Cleanup(func() {
		if data, err := os.ReadFile(marker); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				if child, err := os.FindProcess(pid); err == nil {
					_ = child.Kill()
				}
			}
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(marker); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	start := time.Now()
	report, err := Run(ctx, t.TempDir(), plan.Plan{}, Options{Mode: ModeBuild})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, Code(err))
	require.Equal(t, "failed", report.Status)
	require.Less(t, time.Since(start), 3*time.Second, "cancellation must not wait for the child to close stdout")
}
