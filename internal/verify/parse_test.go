package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// loadLog reads a captured docker build failure from testdata/logs.
func loadLog(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "logs", name))
	require.NoError(t, err)
	return string(data)
}

func TestParseBuildFailureSamples(t *testing.T) {
	for _, tc := range []struct {
		file     string
		wantStep string
		wantMsg  string
		wantHint string
	}{
		{
			file:     "buildkit-npm-ci.log",
			wantStep: "[4/5] RUN npm ci",
			wantMsg:  `process "/bin/sh -c npm ci" did not complete successfully`,
			wantHint: "package-lock.json",
		},
		{
			file:     "buildkit-copy-missing.log",
			wantStep: "[3/4] COPY requirements.txt ./",
			wantMsg:  "not found",
			wantHint: "dockerignore",
		},
		{
			file:     "legacy-npm-build.log",
			wantStep: "Step 4/5: RUN npm install",
			wantMsg:  "non-zero code",
			wantHint: "npm",
		},
		{
			file:     "legacy-copy-failed.log",
			wantStep: "Step 2/4: COPY requirements.txt .",
			wantMsg:  "COPY failed",
			wantHint: "dockerignore",
		},
	} {
		t.Run(tc.file, func(t *testing.T) {
			log := loadLog(t, tc.file)
			f := parseBuildFailure(log)
			require.Equal(t, tc.wantStep, f.Step)
			require.Contains(t, f.Message, tc.wantMsg)
			require.Contains(t, hintFor(log), tc.wantHint)
		})
	}
}

func TestParseBuildFailureNoSignature(t *testing.T) {
	f := parseBuildFailure("#1 [1/2] FROM busybox\n#1 DONE 0.1s\n#2 DONE 0.0s\n")
	require.Empty(t, f.Step)
	require.Empty(t, f.Message)
}

func TestHintFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		log  string
		want string
	}{
		{"daemon", "Cannot connect to the Docker daemon at unix:///var/run/docker.sock", "daemon"},
		{"pull denied", `#4 ERROR: docker.io/library/private:latest: pull access denied`, "Base image pull failed"},
		{"port in use", "Error: listen EADDRINUSE: address already in use :::8080", "Port is already in use"},
		{"go mod", "go: updates to go.mod needed; to update it:", "go mod tidy"},
		{"pip", "ERROR: Could not find a version that satisfies the requirement django==9.9", "Python dependency"},
		{"command missing", `/bin/sh: 1: poetry: not found`, "not installed"},
		{"permission", "mkdir: cannot create directory '/data': Permission denied", "Permission denied"},
		{"disk", "write /var/lib/docker/tmp: no space left on device", "disk space"},
		{"network", "dial tcp: lookup registry-1.docker.io: i/o timeout", "Network failure"},
		{"unknown", "something entirely unrecognizable happened", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, hintFor(tc.log), tc.want)
		})
	}
}

func TestTailLines(t *testing.T) {
	long := ""
	for i := 0; i < 50; i++ {
		long += "line\n"
	}
	require.Len(t, strings.Split(tailLines(long, 30), "\n"), 30)
	require.Equal(t, "a\nb", tailLines("a\nb\n", 30))
	require.Equal(t, "a\nb", tailLines("a\nb", 30))
}

func TestTailWriter(t *testing.T) {
	w := newTailWriter(10)
	_, err := w.Write([]byte("hello"))
	require.NoError(t, err)
	_, err = w.Write([]byte(" world!"))
	require.NoError(t, err)
	require.Equal(t, "llo world!", w.String())

	w = newTailWriter(4)
	_, err = w.Write([]byte("bigger-than-max"))
	require.NoError(t, err)
	require.Equal(t, "-max", w.String())
}
