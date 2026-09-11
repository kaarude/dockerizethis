package verify

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultDockerfile is used by Smoke, which builds dir/Dockerfile.
const DefaultDockerfile = "Dockerfile"

// Result describes the outcome of a docker build. It is populated even on
// failure whenever docker itself ran, so callers can report diagnostics.
type Result struct {
	OK         bool   `json:"ok"`
	Image      string `json:"image,omitempty"`
	Dockerfile string `json:"dockerfile"`
	DurationMs int64  `json:"durationMs"`
	// FailedStep is the engine-native label of the failing instruction,
	// e.g. "[4/5] RUN npm ci" (BuildKit) or "Step 4/5: RUN npm ci" (legacy).
	FailedStep string `json:"failedStep,omitempty"`
	// Stderr holds the last ~30 lines of the build log: stderr under
	// BuildKit, stdout under the legacy builder which logs there instead.
	Stderr string `json:"stderr,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// Build runs `docker build` in dir tagging the image for later reuse by
// Smoke. dockerfilePath resolves relative to dir. It returns a friendly
// ErrDockerUnavailable when the docker CLI or daemon is missing, and an
// ErrBuildFailed wrapping the parsed failure otherwise.
func Build(ctx context.Context, dir, dockerfilePath string) (Result, error) {
	res := Result{Dockerfile: dockerfilePath}
	if res.Dockerfile == "" {
		res.Dockerfile = DefaultDockerfile
	}
	if err := ensureDocker(ctx); err != nil {
		return res, err
	}
	res.Image = imageTag(dir)

	var stdout, stderr = newTailWriter(64 << 10), newTailWriter(64 << 10)
	// The build context is dir; a relative dockerfilePath resolves inside
	// it because the command runs with Dir=dir. Output is piped, so
	// BuildKit emits the plain progress format the parser expects.
	cmd := exec.CommandContext(ctx, "docker", "build", "-f", res.Dockerfile, "-t", res.Image, ".")
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = stdout, stderr

	start := time.Now()
	err := cmd.Run()
	res.DurationMs = time.Since(start).Milliseconds()
	if err == nil {
		res.OK = true
		return res, nil
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}

	log := stderr.String() + "\n" + stdout.String()
	if reDaemon.MatchString(log) {
		return res, fmt.Errorf("%w: %s", ErrDockerUnavailable, oneLine(log))
	}
	f := parseBuildFailure(log)
	res.FailedStep = f.Step
	res.Stderr = tailLines(log, 30)
	res.Hint = hintFor(log)
	if f.Message == "" {
		f.Message = "docker build exited non-zero"
	}
	return res, fmt.Errorf("%w: %s", ErrBuildFailed, f.Message)
}

// ensureDocker reports ErrDockerUnavailable unless the docker CLI is on
// PATH and its daemon answers.
func ensureDocker(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("%w: docker CLI not found in PATH (see https://docs.docker.com/get-docker/)", ErrDockerUnavailable)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(pingCtx, "docker", "info").CombinedOutput(); err != nil {
		return fmt.Errorf("%w: docker daemon is not reachable: %s", ErrDockerUnavailable, oneLine(string(out)))
	}
	return nil
}

// imageTag derives a deterministic, valid docker tag from dir so repeated
// builds hit the layer cache and Smoke finds the image Build produced.
func imageTag(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	sum := sha256.Sum256([]byte(abs))
	var b strings.Builder
	for _, r := range strings.ToLower(filepath.Base(abs)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	// Tags must start with an alphanumeric or underscore.
	name := strings.TrimLeft(b.String(), ".-")
	if name == "" {
		name = "app"
	}
	if len(name) > 40 {
		name = name[:40]
	}
	return fmt.Sprintf("dockerizethis-verify:%s-%x", name, sum[:4])
}
