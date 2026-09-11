package emit

import (
	"regexp"
	"sort"
	"strings"

	"github.com/carl/dockerizethis/internal/plan"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const envExamplePath = ".env.example"

// EnvExample renders a .env.example for p.Env with variables sorted by name,
// hints and required markers as comments, and every value left empty. It never
// reads a real .env. Invalid variable names are omitted; every hint line is a comment.
func EnvExample(p plan.Plan) File {
	env := append([]plan.EnvVar(nil), p.Env...)
	sort.SliceStable(env, func(i, j int) bool { return env[i].Name < env[j].Name })

	var b strings.Builder
	for _, v := range env {
		if !envNamePattern.MatchString(v.Name) {
			continue
		}
		if v.Hint != "" {
			for _, line := range strings.Split(strings.ReplaceAll(v.Hint, "\r", "\n"), "\n") {
				b.WriteString("# ")
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
		if v.Required {
			b.WriteString("# required\n")
		}
		b.WriteString(v.Name)
		b.WriteString("=\n")
	}
	return File{Path: envExamplePath, Content: []byte(b.String()), Mode: defaultFileMode}
}
