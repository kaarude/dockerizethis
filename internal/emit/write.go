package emit

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const defaultFileMode fs.FileMode = 0o644

// diffWriter receives dry-run previews. Tests replace it to capture output.
var diffWriter io.Writer = os.Stdout

// SetDiffWriter redirects dry-run diff previews, returning the previous
// writer so callers can restore it. Callers use it to keep stdout clean for
// structured output such as --json reports.
func SetDiffWriter(w io.Writer) io.Writer {
	prev := diffWriter
	diffWriter = w
	return prev
}

// write implements Write: it writes each file under root, leaving existing
// files untouched unless opts.Force or opts.Backup allows a replacement.
// With opts.DryRun nothing is written and a line diff is printed per file.
//
// Files earlier in the slice may already be written when a later file fails;
// the results returned alongside the error describe those earlier writes.
func write(root string, files []File, opts Options) ([]Result, error) {
	rels := make([]string, len(files))
	for i, f := range files {
		_, rel, err := resolveTarget(root, f.Path)
		if err != nil {
			return nil, err
		}
		rels[i] = rel
	}

	if len(files) == 0 {
		return []Result{}, nil
	}
	if !opts.DryRun {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, fmt.Errorf("emit: create root: %w", err)
		}
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		if opts.DryRun && errors.Is(err, fs.ErrNotExist) {
			results := make([]Result, 0, len(files))
			for i, f := range files {
				if err := printDiff(f.Path, nil, f.Content); err != nil {
					return results, err
				}
				results = append(results, Result{Path: rels[i], Action: "would-create"})
			}
			return results, nil
		}
		return nil, fmt.Errorf("emit: open root: %w", err)
	}
	defer func() { _ = dir.Close() }()

	results := make([]Result, 0, len(files))
	for i, f := range files {
		action, err := apply(dir, f, rels[i], opts)
		if err != nil {
			return results, err
		}
		results = append(results, Result{Path: rels[i], Action: action})
	}
	return results, nil
}

// apply uses a directory handle so symlinks cannot redirect writes outside root.
func apply(root *os.Root, file File, target string, opts Options) (string, error) {
	info, err := root.Lstat(target)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("emit: stat %s: %w", target, err)
	}
	if exists && !info.Mode().IsRegular() {
		return "", fmt.Errorf("emit: target %s is not a regular file", target)
	}
	if exists && !opts.Force && !opts.Backup {
		return "skipped-exists", nil
	}
	if opts.DryRun {
		var old []byte
		if exists {
			old, err = root.ReadFile(target)
			if err != nil {
				return "", fmt.Errorf("emit: read %s: %w", target, err)
			}
		}
		if err := printDiff(file.Path, old, file.Content); err != nil {
			return "", err
		}
		return "would-create", nil
	}

	tmp, err := stageFile(root, target, file)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Remove(tmp) }()
	if !exists {
		// Linking publishes atomically without replacing a concurrent writer's file.
		if err := root.Link(tmp, target); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return "skipped-exists", nil
			}
			return "", fmt.Errorf("emit: create %s: %w", target, err)
		}
		return "created", nil
	}
	if opts.Backup {
		// Keep the original in place until its complete replacement is ready.
		// Link refuses to replace an earlier backup.
		if err := root.Link(target, target+".bak"); err != nil {
			return "", fmt.Errorf("emit: back up %s: %w", target, err)
		}
	}
	if err := root.Rename(tmp, target); err != nil {
		return "", fmt.Errorf("emit: replace %s: %w", target, err)
	}
	if opts.Backup {
		return "backed-up", nil
	}
	return "created", nil
}

// stageFile creates a complete temporary file beside the destination.
func stageFile(root *os.Root, target string, file File) (string, error) {
	dir := filepath.Dir(target)
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("emit: create directory %s: %w", dir, err)
	}
	tmpName := filepath.Join(dir, ".dockerizethis-"+rand.Text()+".tmp")
	tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("emit: create temporary file for %s: %w", target, err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = tmp.Close()
			_ = root.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(file.Content); err != nil {
		return "", fmt.Errorf("emit: write %s: %w", target, err)
	}
	if err := tmp.Chmod(permissions(file.Mode)); err != nil {
		return "", fmt.Errorf("emit: chmod %s: %w", target, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("emit: close %s: %w", target, err)
	}
	complete = true
	return tmpName, nil
}

// permissions returns mode's permission bits, defaulting to 0644.
func permissions(mode fs.FileMode) fs.FileMode {
	if perm := mode.Perm(); perm != 0 {
		return perm
	}
	return defaultFileMode
}

// resolveTarget joins name onto root and rejects names that escape root.
func resolveTarget(root, name string) (target, rel string, err error) {
	clean := filepath.Clean(name)
	if clean == "." || filepath.IsAbs(clean) {
		return "", "", fmt.Errorf("emit: invalid artifact path %q", name)
	}
	target = filepath.Join(root, clean)
	rel, err = filepath.Rel(root, target)
	if err != nil {
		return "", "", fmt.Errorf("emit: resolve %q: %w", name, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("emit: path %q escapes root", name)
	}
	return target, filepath.ToSlash(rel), nil
}

// printDiff writes a minimal line diff between old and new: shared leading and
// trailing lines are elided, the rest is printed with - and + prefixes.
func printDiff(path string, old, new []byte) error {
	var output strings.Builder
	fmt.Fprintf(&output, "--- %s (current)\n+++ %s (generated)\n", path, path)
	oldLines, newLines := splitLines(old), splitLines(new)

	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}

	for _, line := range oldLines[prefix : len(oldLines)-suffix] {
		fmt.Fprintf(&output, "-%s\n", line)
	}
	for _, line := range newLines[prefix : len(newLines)-suffix] {
		fmt.Fprintf(&output, "+%s\n", line)
	}
	if _, err := io.WriteString(diffWriter, output.String()); err != nil {
		return fmt.Errorf("emit: print diff for %s: %w", path, err)
	}
	return nil
}

func splitLines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}
