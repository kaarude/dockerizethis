package emit

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
		{name: "force overwrites", opts: Options{Force: true}, wantAction: "created", wantContent: "new\n"},
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
			if tc.wantAction == "created" || tc.wantAction == "backed-up" {
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
	}}, Options{DryRun: true, Force: true})

	require.NoError(t, err)
	require.Equal(t, []Result{{Path: "Dockerfile", Action: "would-create"}}, results)
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

func TestWriteRejectsSymlinkEscape(t *testing.T) {
	for _, opts := range []Options{{}, {Force: true}, {Backup: true}, {DryRun: true, Force: true}} {
		root, outside := t.TempDir(), t.TempDir()
		require.NoError(t, os.Symlink(outside, filepath.Join(root, "nested")))
		_, err := Write(root, []File{{Path: "nested/Dockerfile", Content: []byte("new")}}, opts)
		require.Error(t, err)
		require.NoFileExists(t, filepath.Join(outside, "Dockerfile"))
	}
}

func TestWriteBackupCollisionPreservesBothFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Dockerfile")
	require.NoError(t, os.WriteFile(target, []byte("current"), 0o644))
	require.NoError(t, os.WriteFile(target+".bak", []byte("original"), 0o644))
	_, err := Write(root, []File{{Path: "Dockerfile", Content: []byte("new")}}, Options{Backup: true})
	require.Error(t, err)
	require.Equal(t, "current", readFile(t, target))
	require.Equal(t, "original", readFile(t, target+".bak"))
	leftovers, err := filepath.Glob(filepath.Join(root, ".dockerizethis-*"))
	require.NoError(t, err)
	require.Empty(t, leftovers)
}

func TestWriteRejectsNonregularTargets(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "Dockerfile")
			if kind == "directory" {
				require.NoError(t, os.Mkdir(target, 0o755))
			} else {
				require.NoError(t, os.WriteFile(filepath.Join(root, "original"), []byte("keep"), 0o644))
				require.NoError(t, os.Symlink("original", target))
			}
			_, err := Write(root, []File{{Path: "Dockerfile", Content: []byte("new")}}, Options{Backup: true})
			require.Error(t, err)
			require.NoFileExists(t, target+".bak")
			info, err := os.Lstat(target)
			require.NoError(t, err)
			require.False(t, info.Mode().IsRegular())
		})
	}
}

func TestWriteDryRunSkipsExistingWithoutOverwriteOptions(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Dockerfile")
	require.NoError(t, os.WriteFile(target, []byte("keep"), 0o644))
	results, err := Write(root, []File{{Path: "Dockerfile", Content: []byte("new")}}, Options{DryRun: true})
	require.NoError(t, err)
	require.Equal(t, []Result{{Path: "Dockerfile", Action: "skipped-exists"}}, results)
	require.Equal(t, "keep", readFile(t, target))
}

func TestWriteDryRunDoesNotCreateRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	results, err := Write(root, []File{{Path: "Dockerfile", Content: []byte("new")}}, Options{DryRun: true})
	require.NoError(t, err)
	require.Equal(t, []Result{{Path: "Dockerfile", Action: "would-create"}}, results)
	require.NoDirExists(t, root)
}

func TestWriteConcurrentCreationDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	type outcome struct {
		results []Result
		err     error
		content string
	}
	const writers = 16
	outcomes := make(chan outcome, writers)
	start := make(chan struct{})
	for i := range writers {
		go func() {
			<-start
			content := strings.Repeat("x", i+1)
			results, err := Write(root, []File{{Path: "Dockerfile", Content: []byte(content)}}, Options{})
			outcomes <- outcome{results, err, content}
		}()
	}
	close(start)
	created := 0
	var winningContent string
	for range writers {
		result := <-outcomes
		require.NoError(t, result.err)
		require.Len(t, result.results, 1)
		switch result.results[0].Action {
		case "created":
			created++
			winningContent = result.content
		case "skipped-exists":
		default:
			t.Fatalf("unexpected action %q", result.results[0].Action)
		}
	}
	require.Equal(t, 1, created)
	require.Equal(t, winningContent, readFile(t, filepath.Join(root, "Dockerfile")))
}
