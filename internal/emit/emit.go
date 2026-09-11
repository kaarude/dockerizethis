package emit

import (
    "io/fs"
)

type File struct {
    Path    string
    Content []byte
    Mode    fs.FileMode
}

type Options struct {
    DryRun bool
    Force  bool
    Backup bool
}

type Result struct {
    Path    string `json:"path"`
    Action  string `json:"action"` // "created" | "skipped-exists" | "backed-up" | "would-create"
}

// Write applies files under root per opts. Never silently overwrites.
func Write(root string, files []File, opts Options) ([]Result, error) {
    return write(root, files, opts)
}
