package main

import (
	"bytes"
	"encoding/json"
	"testing"

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

func TestRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		path string
	}{
		{name: "default path", path: "."},
		{name: "explicit path", args: []string{"project"}, path: "project"},
		{name: "all flags", args: []string{"project", "--dry-run", "--yes", "--verify=full", "--stack=go", "--service=api", "--force", "--backup"}, path: "project"},
		{name: "no verification", args: []string{"--verify=none"}, path: "."},
		{name: "build verification", args: []string{"--verify=build"}, path: "."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newRootCommand()
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetArgs(append(append([]string{}, tc.args...), "--json"))
			require.NoError(t, cmd.Execute())
			var report map[string]string
			require.NoError(t, json.Unmarshal(output.Bytes(), &report))
			require.Equal(t, tc.path, report["path"])
			require.Contains(t, report["message"], "not implemented")
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
