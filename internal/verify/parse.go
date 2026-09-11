package verify

import (
	"regexp"
	"strings"
)

var (
	// BuildKit prefixes every line with "#N "; step headers carry a
	// "[stage x/y] INSTRUCTION" label, output lines a timestamp instead.
	reBKStepLabel = regexp.MustCompile(`^#(\d+) (\[[^\]]+\] .+?)\s*$`)
	reBKStepError = regexp.MustCompile(`^#(\d+) (?:\d+(?:\.\d+)? )?ERROR: (.+?)\s*$`)
	// The "------" error block repeats the failing step as " > [x/y] ...:".
	reBKBlockStep = regexp.MustCompile(`^ > (\[[^\]]+\].*):\s*$`)
	reBKSolve     = regexp.MustCompile(`^ERROR: (.+?)\s*$`)

	reLegacyStep = regexp.MustCompile(`^Step (\d+)/(\d+)\s*:\s*(.+?)\s*$`)
	reLegacyFail = regexp.MustCompile(`(?i)returned a non-zero code|^(?:COPY|ADD) failed`)
)

// failure is the parsed signature of a failed docker build.
type failure struct {
	Step    string // engine-native step label, e.g. "[4/5] RUN npm ci" or "Step 4/5: RUN npm ci"
	Message string // best single-line summary of the error
}

// parseBuildFailure scans build output for BuildKit and legacy signatures.
// A zero failure means the log held no recognizable failure.
func parseBuildFailure(log string) failure {
	var f failure
	var lastLegacy string
	labels := map[string]string{}
	for _, raw := range strings.Split(log, "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := reBKStepLabel.FindStringSubmatch(line); m != nil {
			labels[m[1]] = m[2]
			continue
		}
		if m := reBKStepError.FindStringSubmatch(line); m != nil {
			if f.Step == "" {
				f.Step = labels[m[1]]
			}
			if f.Message == "" {
				f.Message = m[2]
			}
			continue
		}
		if m := reBKBlockStep.FindStringSubmatch(line); m != nil {
			f.Step = m[1] // authoritative label, overrides the #NN lookup
			continue
		}
		if m := reBKSolve.FindStringSubmatch(line); m != nil {
			f.Message = m[1] // final "failed to solve" line is the best summary
			continue
		}
		if m := reLegacyStep.FindStringSubmatch(line); m != nil {
			lastLegacy = "Step " + m[1] + "/" + m[2] + ": " + m[3]
			continue
		}
		if reLegacyFail.MatchString(line) {
			if f.Step == "" {
				f.Step = lastLegacy
			}
			if f.Message == "" {
				f.Message = strings.TrimSpace(line)
			}
		}
	}
	return f
}

// hintRule maps a log signature to actionable advice. First match wins.
type hintRule struct {
	re   *regexp.Regexp
	hint string
}

var hintRules = []hintRule{
	{reDaemon, "Docker daemon is not reachable — start Docker and retry."},
	{regexp.MustCompile(`(?i)pull access denied|manifest unknown|authentication required|unauthorized:`),
		"Base image pull failed — check the image name and tag, or log in to the registry."},
	{regexp.MustCompile(`(?i)dockerfile parse error|failed to parse dockerfile|unknown instruction`),
		"Dockerfile syntax error — check the reported line."},
	{regexp.MustCompile(`(?i)(COPY|ADD) failed|failed to (compute cache key|calculate checksum)|lstat .*no such file|": not found`),
		"A COPY/ADD source is missing from the build context — verify COPY/ADD paths and .dockerignore."},
	{regexp.MustCompile(`(?i)EADDRINUSE|address already in use|port is already allocated|bind: address|ports are not available`),
		"Port is already in use — stop the conflicting process or change the exposed port."},
	{regexp.MustCompile(`npm ERR! `),
		"npm install failed — ensure package-lock.json is committed and in sync with package.json (run `npm install` locally to refresh it)."},
	{regexp.MustCompile(`(?i)yarn.*(frozen-lockfile|lockfile needs to be updated)|YN0028`),
		"Yarn install failed — update and commit yarn.lock."},
	{regexp.MustCompile(`ERR_PNPM_OUTDATED_LOCKFILE|pnpm.*lockfile`),
		"pnpm install failed — update and commit pnpm-lock.yaml."},
	{regexp.MustCompile(`go: (updates to go\.mod needed|missing go\.sum entry|no required module provides)`),
		"Go module resolution failed — run `go mod tidy` and commit go.mod and go.sum."},
	{regexp.MustCompile(`(?i)no matching distribution|could not find a version|ModuleNotFoundError`),
		"Python dependency install failed — check requirements/constraints and the Python version."},
	{regexp.MustCompile(`(?i)command not found|/bin/\w*sh:.*: not found`),
		"A command is not installed in the image — check the base image or add an install step."},
	{regexp.MustCompile(`(?i)permission denied`),
		"Permission denied — check file ownership and the Dockerfile USER instruction."},
	{regexp.MustCompile(`(?i)no space left on device`),
		"Docker ran out of disk space — prune with `docker system prune` and retry."},
	{regexp.MustCompile(`(?i)i/o timeout|TLS handshake timeout|temporary failure in name resolution|network is unreachable|dial tcp`),
		"Network failure during build — check connectivity, proxies, or registry availability."},
}

var reDaemon = regexp.MustCompile(`(?i)cannot connect to the docker daemon|is the docker daemon running|docker daemon is not running|error during connect`)

// hintFor returns remediation advice for the first matching signature in
// the build output, or "" when nothing recognized is present.
func hintFor(log string) string {
	for _, r := range hintRules {
		if r.re.MatchString(log) {
			return r.hint
		}
	}
	return ""
}

// tailLines returns the last n lines of s, with trailing blank lines
// trimmed first.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// tailWriter is an io.Writer that retains only the last max bytes written.
type tailWriter struct {
	buf []byte
	max int
}

func newTailWriter(max int) *tailWriter { return &tailWriter{max: max} }

func (w *tailWriter) Write(p []byte) (int, error) {
	if len(p) >= w.max {
		w.buf = append(w.buf[:0], p[len(p)-w.max:]...)
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = append(w.buf[:0], w.buf[len(w.buf)-w.max:]...)
	}
	return len(p), nil
}

func (w *tailWriter) String() string { return string(w.buf) }

// oneLine returns the last non-empty line of s, truncated to 200 chars,
// for embedding raw command output into an error message.
func oneLine(s string) string {
	line := ""
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if t := strings.TrimSpace(l); t != "" {
			line = t
		}
	}
	if len(line) > 200 {
		line = line[:200] + "…"
	}
	return line
}
