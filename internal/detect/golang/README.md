# Go detection

`golang.Detector{}` implements `detect.Detector` and reports stack `go`.
It reads go.mod and parses Go source with the standard library. It does not
execute the project. Vendor, testdata, nested modules, test files, and source
symlinks are excluded.

The plan includes the module's binary name, Go version, main package build
command, imported frameworks and services, and literal `os.Getenv` names.
Web ports come from PORT/ADDR fallback assignments, otherwise 8080.
A missing Go directive defaults to 1.26 with a note. Multiple main packages
produce a low-confidence plan that requires an explicit build package.

`Extras["buildPackage"]` carries a main package outside the root.
`Extras["cgo"]="1"` records an import of C. Build tags are not evaluated, and
native libraries beyond the renderer's standard compiler/runtime packages
must be reviewed for each project.
