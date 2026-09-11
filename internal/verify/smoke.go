package verify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/carl/dockerizethis/internal/plan"
)

const (
	// smokeTimeout bounds the health-check poll, per the verify spec.
	smokeTimeout  = 20 * time.Second
	smokeInterval = 500 * time.Millisecond
)

// SmokeResult describes the outcome of a container smoke test.
type SmokeResult struct {
	OK      bool `json:"ok"`
	Skipped bool `json:"skipped,omitempty"`
	// Reason explains a skip, e.g. a non-web process type.
	Reason string `json:"reason,omitempty"`
	Image  string `json:"image,omitempty"`
	// Build is the result of the image build Smoke performed first.
	Build       *Result `json:"build,omitempty"`
	ContainerID string  `json:"containerId,omitempty"`
	// URL is the localhost health-check URL actually polled, on the
	// ephemeral host port docker assigned.
	URL        string `json:"url,omitempty"`
	StatusCode int    `json:"statusCode,omitempty"`
	Attempts   int    `json:"attempts,omitempty"`
	// Logs holds the tail of `docker logs` captured on failure.
	Logs string `json:"logs,omitempty"`
	Hint string `json:"hint,omitempty"`
}

// Smoke builds dir/Dockerfile, runs the image detached, and polls
// http://localhost:<mapped-port><HealthPath> until the app answers with a
// status below 500 or smokeTimeout elapses. It returns a skipped result —
// not an error — for non-web plans, since workers and static sites expose
// no HTTP endpoint.
//
// The container is deliberately started without the plan's env vars
// (their values are unknown at verify time); only PORT is injected from
// plan.Port. The host port is ephemeral to avoid collisions. The
// container is always killed and removed before Smoke returns.
func Smoke(ctx context.Context, dir string, p plan.Plan) (SmokeResult, error) {
	var res SmokeResult
	if p.Process != plan.ProcessWeb {
		res.Skipped = true
		res.Reason = fmt.Sprintf("smoke test only applies to web processes, plan has %q", p.Process)
		return res, nil
	}
	if p.Port <= 0 {
		return res, errors.New("smoke test requires plan.Port to be set for a web process")
	}
	if err := ensureDocker(ctx); err != nil {
		return res, err
	}

	build, err := Build(ctx, dir, DefaultDockerfile)
	res.Build = &build
	res.Image = build.Image
	if err != nil {
		return res, err // already carries ErrBuildFailed or ErrDockerUnavailable
	}

	name := "dockerizethis-smoke-" + randHex()
	out, err := exec.CommandContext(ctx, "docker", "run", "-d",
		"--name", name,
		"-e", "PORT="+strconv.Itoa(p.Port),
		"-p", "127.0.0.1::"+strconv.Itoa(p.Port),
		res.Image).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if reDaemon.Match(out) {
			return res, fmt.Errorf("%w: %s", ErrDockerUnavailable, oneLine(string(out)))
		}
		res.Hint = hintFor(string(out))
		return res, fmt.Errorf("%w: docker run: %s", ErrSmokeFailed, oneLine(string(out)))
	}
	id := strings.TrimSpace(string(out))
	res.ContainerID = id
	defer removeContainer(id)

	hostPort, err := mappedPort(ctx, id, p.Port)
	if err != nil {
		res.Logs = tailLines(containerLogs(ctx, id), 30)
		res.Hint = "could not determine the published port — the container may have exited immediately"
		return res, fmt.Errorf("%w: %s", ErrSmokeFailed, err)
	}

	healthPath := p.HealthPath
	if !strings.HasPrefix(healthPath, "/") {
		healthPath = "/" + healthPath
	}
	res.URL = "http://127.0.0.1:" + hostPort + healthPath
	return res, res.poll(ctx)
}

// poll queries res.URL until the app responds with status < 500, the
// container dies, the context ends, or smokeTimeout elapses.
func (res *SmokeResult) poll(ctx context.Context) error {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(smokeTimeout)
	for {
		res.Attempts++
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, res.URL, nil)
		if err == nil {
			if resp, err := client.Do(req); err == nil {
				res.StatusCode = resp.StatusCode
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode < 500 {
					res.OK = true
					return nil
				}
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !containerRunning(ctx, res.ContainerID) {
			res.Logs = tailLines(containerLogs(ctx, res.ContainerID), 30)
			res.Hint = "container exited before the health check passed — inspect Logs and the image's start command"
			return fmt.Errorf("%w: container exited before answering the health check", ErrSmokeFailed)
		}
		if time.Now().After(deadline) {
			res.Logs = tailLines(containerLogs(ctx, res.ContainerID), 30)
			res.Hint = "no HTTP response within " + smokeTimeout.String() + " — check the app listens on the plan's port and HealthPath"
			return fmt.Errorf("%w: health check timed out after %s", ErrSmokeFailed, smokeTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(smokeInterval):
		}
	}
}

// mappedPort returns the host port docker published for containerPort.
func mappedPort(ctx context.Context, id string, containerPort int) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "port", id,
		strconv.Itoa(containerPort)+"/tcp").Output()
	if err != nil {
		return "", fmt.Errorf("docker port: %w", err)
	}
	// Output lines look like "127.0.0.1:49153" or "[::]:49153".
	first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if i := strings.LastIndex(first, ":"); i >= 0 {
		return first[i+1:], nil
	}
	return "", fmt.Errorf("docker port: unexpected output %q", first)
}

func containerRunning(ctx context.Context, id string) bool {
	out, err := exec.CommandContext(ctx, "docker", "inspect",
		"-f", "{{.State.Running}}", id).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func containerLogs(ctx context.Context, id string) string {
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(lctx, "docker", "logs", "--tail", "60", id).CombinedOutput()
	return string(out)
}

// removeContainer force-removes the smoke container. It runs on a fresh
// context because the caller's may already be canceled.
func removeContainer(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", id).Run()
}

func randHex() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
