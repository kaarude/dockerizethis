package emit

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func TestWriteExistingFileMatrix(t *testing.T) {
	tests := []struct {
		name        string
		opts        Options
		wantAction  string
		wantContent string
		wantBackup  string
	}{
		{name: "no flags skips", opts: Options{}, wantAction: "skipped-exists", wantContent: "old\n"},
		{name: "force overwrites", opts: Options{Force: true}, wantAction: "overwritten", wantContent: "new\n"},
		{name: "backup preserves original", opts: Options{Backup: true}, wantAction: "backed-up", wantContent: "new\n", wantBackup: "old\n"},
		{name: "backup wins over force", opts: Options{Force: true, Backup: true}, wantAction: "backed-up", wantContent: "new\n", wantBackup: "old\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "Dockerfile")
			require.NoError(t, os.WriteFile(target, []byte("old\n"), 0o644))

			results, err := Write(root, []File{{
				Path:    "Dockerfile",
				Content: []byte("new\n"),
				Mode:    0o600,
			}}, tc.opts)

			require.NoError(t, err)
			require.Equal(t, []Result{{Path: "Dockerfile", Action: tc.wantAction}}, results)
			require.Equal(t, tc.wantContent, readFile(t, target))
			if tc.wantBackup == "" {
				require.NoFileExists(t, target+".bak")
			} else {
				require.Equal(t, tc.wantBackup, readFile(t, target+".bak"))
			}
			if tc.wantAction == "overwritten" || tc.wantAction == "backed-up" {
				info, err := os.Stat(target)
				require.NoError(t, err)
				require.Equal(t, fs.FileMode(0o600), info.Mode().Perm())
			}
		})
	}
}

func TestWriteCreatesParentDirsAndDefaultsMode(t *testing.T) {
	root := t.TempDir()

	results, err := Write(root, []File{{
		Path:    filepath.Join("nested", "Dockerfile"),
		Content: []byte("FROM scratch\n"),
	}}, Options{})

	require.NoError(t, err)
	require.Equal(t, []Result{{Path: "nested/Dockerfile", Action: "created"}}, results)
	info, err := os.Stat(filepath.Join(root, "nested", "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o644), info.Mode().Perm())
	require.Equal(t, "FROM scratch\n", readFile(t, filepath.Join(root, "nested", "Dockerfile")))
}

func TestWriteContinuesAfterSkip(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep"), []byte("old"), 0o644))

	results, err := Write(root, []File{
		{Path: "keep", Content: []byte("new")},
		{Path: "fresh", Content: []byte("new")},
	}, Options{})

	require.NoError(t, err)
	require.Equal(t, []Result{
		{Path: "keep", Action: "skipped-exists"},
		{Path: "fresh", Action: "created"},
	}, results)
	require.Equal(t, "old", readFile(t, filepath.Join(root, "keep")))
	require.Equal(t, "new", readFile(t, filepath.Join(root, "fresh")))
}

func TestWriteDryRun(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "Dockerfile")
	require.NoError(t, os.WriteFile(existing, []byte("FROM old\nRUN old\n"), 0o644))

	var out bytes.Buffer
	previous := diffWriter
	diffWriter = &out
	t.Cleanup(func() { diffWriter = previous })

	results, err := Write(root, []File{{
		Path:    "Dockerfile",
		Content: []byte("FROM new\nRUN new\n"),
	}}, Options{DryRun: true})

	require.NoError(t, err)
	require.Equal(t, []Result{{Path: "Dockerfile", Action: "would-overwrite"}}, results)
	require.Contains(t, out.String(), "-FROM old")
	require.Contains(t, out.String(), "+FROM new")
	require.Equal(t, "FROM old\nRUN old\n", readFile(t, existing))

	out.Reset()
	results, err = Write(root, []File{{
		Path:    filepath.Join("nested", "compose.yaml"),
		Content: []byte("services:\n"),
	}}, Options{DryRun: true})

	require.NoError(t, err)
	require.Equal(t, []Result{{Path: "nested/compose.yaml", Action: "would-create"}}, results)
	require.Contains(t, out.String(), "+services:")
	require.NoFileExists(t, filepath.Join(root, "nested", "compose.yaml"))
	require.NoDirExists(t, filepath.Join(root, "nested"))
}

func TestWriteAtomicOnError(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "Dockerfile")
	require.NoError(t, os.Mkdir(blocked, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(blocked, "inside"), []byte("data"), 0o644))

	_, err := Write(root, []File{{Path: "Dockerfile", Content: []byte("new\n")}}, Options{Force: true})

	require.Error(t, err)
	info, statErr := os.Stat(blocked)
	require.NoError(t, statErr)
	require.True(t, info.IsDir())
	require.Equal(t, "data", readFile(t, filepath.Join(blocked, "inside")))

	leftovers, globErr := filepath.Glob(filepath.Join(root, ".dockerizethis-*"))
	require.NoError(t, globErr)
	require.Empty(t, leftovers)
}

func TestWriteRejectsEscapingPaths(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "escaped")

	results, err := Write(root, []File{{
		Path:    filepath.Join("..", "escaped"),
		Content: []byte("x"),
	}}, Options{Force: true})

	require.Error(t, err)
	require.Nil(t, results)
	require.NoFileExists(t, outside)
}
