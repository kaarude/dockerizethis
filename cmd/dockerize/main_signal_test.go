package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Exercise main in a separate process so its signal handler and exit code run.
func TestMainSIGTERMCleansUp(t *testing.T) {
	if os.Getenv("DOCKERIZE_TEST_SIGNAL_CHILD") == "1" {
		os.Args = []string{"dockerize", os.Getenv("DOCKERIZE_TEST_PROJECT"), "--yes", "--verify=full", "--json"}
		main()
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix signals and a shell")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	bin := t.TempDir()
	marker, calls := filepath.Join(bin, "polling"), filepath.Join(bin, "calls")
	script := `#!/bin/sh
printf '%s\n' "$1" >> "$DOCKERIZE_TEST_CALLS"
case "$1" in
 info|build|run|rm) exit 0;;
 port) printf '%s\n' "$DOCKERIZE_TEST_ADDRESS";;
 inspect) printf ready > "$DOCKERIZE_TEST_MARKER"; printf true;;
 *) exit 1;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755))
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestMainSIGTERMCleansUp$")
	cmd.Env = append(os.Environ(), "PATH="+bin, "DOCKERIZE_TEST_SIGNAL_CHILD=1",
		"DOCKERIZE_TEST_PROJECT="+fixture(t, "go-http"), "DOCKERIZE_TEST_CALLS="+calls,
		"DOCKERIZE_TEST_MARKER="+marker, "DOCKERIZE_TEST_ADDRESS="+strings.TrimPrefix(server.URL, "http://"))
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	err = cmd.Wait()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, stderr.String())
	require.Equal(t, 1, exit.ExitCode(), stderr.String())
	var r report
	require.NoError(t, json.Unmarshal(out.Bytes(), &r))
	require.Equal(t, "failed", r.Verify.Status)
	require.Contains(t, r.Verify.Error, "context canceled")
	commands, err := os.ReadFile(calls)
	require.NoError(t, err)
	require.Contains(t, string(commands), "run\n")
	require.Contains(t, string(commands), "rm\n", "SIGTERM must reach container cleanup")
}
