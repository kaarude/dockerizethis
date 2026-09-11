package emit

import (
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

// write implements Write: it writes each file under root, leaving existing
// files untouched unless opts.Force or opts.Backup allows a replacement.
// With opts.DryRun nothing is written and a line diff is printed per file.
//
// Files earlier in the slice may already be written when a later file fails;
// the results returned alongside the error describe those earlier writes.
func write(root string, files []File, opts Options) ([]Result, error) {
	targets := make([]string, len(files))
	rels := make([]string, len(files))
	for i, f := range files {
		target, rel, err := resolveTarget(root, f.Path)
		if err != nil {
			return nil, err
		}
		targets[i], rels[i] = target, rel
	}

	results := make([]Result, 0, len(files))
	for i, f := range files {
		action, err := apply(f, targets[i], opts)
		if err != nil {
			return results, err
		}
		results = append(results, Result{Path: rels[i], Action: action})
	}
	return results, nil
}

// apply applies one File to target and reports the action taken.
func apply(file File, target string, opts Options) (string, error) {
	exists, err := fileExists(target)
	if err != nil {
		return "", err
	}

	switch {
	case opts.DryRun:
		old, err := os.ReadFile(target)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("emit: read %s: %w", target, err)
		}
		printDiff(file.Path, old, file.Content)
		if exists {
			return "would-overwrite", nil
		}
		return "would-create", nil

	case !exists:
		if err := writeFile(target, file); err != nil {
			return "", err
		}
		return "created", nil

	case opts.Backup:
		backup := target + ".bak"
		if err := os.Rename(target, backup); err != nil {
			return "", fmt.Errorf("emit: back up %s: %w", target, err)
		}
		if err := writeFile(target, file); err != nil {
			return "", err
		}
		return "backed-up", nil

	case opts.Force:
		if err := writeFile(target, file); err != nil {
			return "", err
		}
		return "overwritten", nil

	default:
		return "skipped-exists", nil
	}
}

// writeFile writes content to a temporary file beside target and renames it
// into place, so target never holds a partial file.
func writeFile(target string, file File) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("emit: create directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".dockerizethis-*.tmp")
	if err != nil {
		return fmt.Errorf("emit: create temporary file for %s: %w", target, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(file.Content); err != nil {
		tmp.Close()
		return fmt.Errorf("emit: write %s: %w", target, err)
	}
	if err := tmp.Chmod(permissions(file.Mode)); err != nil {
		tmp.Close()
		return fmt.Errorf("emit: chmod %s: %w", target, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("emit: close %s: %w", target, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("emit: rename %s: %w", target, err)
	}
	tmpName = ""
	return nil
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

func fileExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("emit: stat %s: %w", path, err)
}

// printDiff writes a minimal line diff between old and new: shared leading and
// trailing lines are elided, the rest is printed with - and + prefixes.
func printDiff(path string, old, new []byte) {
	fmt.Fprintf(diffWriter, "--- %s (current)\n+++ %s (generated)\n", path, path)
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
		fmt.Fprintf(diffWriter, "-%s\n", line)
	}
	for _, line := range newLines[prefix : len(newLines)-suffix] {
		fmt.Fprintf(diffWriter, "+%s\n", line)
	}
}

func splitLines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}
