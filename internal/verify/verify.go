// Package verify builds generated images and probes web startup with Docker.
package verify

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/carl/dockerizethis/internal/plan"
)

type BuildResult struct {
	Image      string `json:"image,omitempty"`
	Output     string `json:"output,omitempty"`
	FailedStep string `json:"failedStep,omitempty"`
	Hint       string `json:"hint,omitempty"`
}

type SmokeResult struct {
	URL        string `json:"url,omitempty"`
	StatusCode int    `json:"statusCode,omitempty"`
}

type Error struct {
	ExitCode int
	Err      error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Code preserves typed verification failures through wrapping; other failures exit 1.
func Code(err error) int {
	if err == nil {
		return 0
	}
	var failure *Error
	if errors.As(err, &failure) {
		return failure.ExitCode
	}
	return 1
}

func failure(code int, err error) error { return &Error{ExitCode: code, Err: err} }

func docker(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	// A Docker child can inherit the pipes after the CLI exits or is canceled.
	cmd.WaitDelay = time.Second
	output := newTailWriter(64 << 10)
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return strings.TrimSpace(output.String()), err
}

func available(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return failure(1, err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := docker(probeCtx, dir, "info", "--format", "{{.ServerVersion}}"); err != nil {
		if err := ctx.Err(); err != nil {
			return failure(1, err)
		}
		return failure(4, fmt.Errorf("docker unavailable: start Docker and check docker info: %w", err))
	}
	return nil
}

// build retains a uniquely tagged local image. Relative Dockerfile paths are relative to dir.
func build(ctx context.Context, dir, dockerfilePath string) (BuildResult, error) {
	var result BuildResult
	if err := available(ctx, dir); err != nil {
		return result, err
	}
	buildCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	image := "dockerizethis-verify:" + strings.ToLower(rand.Text())
	output, err := docker(buildCtx, dir, "build", "--load", "--progress=plain", "--tag", image, "--file", dockerfilePath, ".")
	result.Output = output
	if err != nil {
		if err := ctx.Err(); err != nil {
			return result, failure(1, err)
		}
		parsed := parseBuildFailure(output)
		result.FailedStep, result.Hint = parsed.Step, hintFor(output)
		code := 2
		if reDaemon.MatchString(output) {
			code = 4
		}
		return result, failure(code, fmt.Errorf("docker build failed: %w\n%s", err, tailLines(output, 30)))
	}
	result.Image = image
	return result, nil
}

// smoke runs the exact image built by Run.
// It uses an existing .env, publishes only on loopback, and always removes its
// temporary container. It does not provision backing services. Docker must be local.
// HealthPath requires 2xx/3xx; without one, any non-5xx response proves HTTP startup.
func smoke(ctx context.Context, dir string, p plan.Plan, image string) (result SmokeResult, runErr error) {
	if p.Process != plan.ProcessWeb {
		return result, failure(3, fmt.Errorf("smoke requires a web process"))
	}
	if p.Port < 1 || p.Port > 65535 {
		return result, failure(3, fmt.Errorf("smoke requires a valid port"))
	}
	if image == "" || strings.HasPrefix(image, "-") {
		return result, failure(3, fmt.Errorf("smoke requires the image from a successful build"))
	}
	path := p.HealthPath
	if path == "" {
		path = "/"
	}
	parsed, err := url.ParseRequestURI(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || parsed.Host != "" {
		return result, failure(3, fmt.Errorf("smoke health path must be a local absolute URL path"))
	}
	if err := available(ctx, dir); err != nil {
		return result, err
	}
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	name := "dockerizethis-smoke-" + strings.ToLower(rand.Text())
	args := []string{"run", "--detach", "--name", name, "--publish", fmt.Sprintf("127.0.0.1::%d", p.Port)}
	if info, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
		if !info.Mode().IsRegular() {
			return result, failure(3, fmt.Errorf(".env must be a regular file"))
		}
		args = append(args, "--env-file", ".env")
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, failure(3, fmt.Errorf("inspect .env: %w", err))
	}
	// Register cleanup before starting: cancellation can occur after Docker creates the container.
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		output, err := docker(cleanup, dir, "rm", "--force", name)
		if err != nil && !strings.Contains(output, "No such container") {
			runErr = errors.Join(runErr, failure(3, fmt.Errorf("remove temporary container %s: %w", name, err)))
		}
		if err := parentCtx.Err(); err != nil {
			runErr = errors.Join(failure(1, err), runErr)
		}
	}()
	if _, err := docker(ctx, dir, append(args, image)...); err != nil {
		return result, failure(3, fmt.Errorf("start smoke container: %w; check .env and runtime configuration", err))
	}
	address, err := docker(ctx, dir, "port", name, strconv.Itoa(p.Port)+"/tcp")
	if err != nil {
		return result, failure(3, fmt.Errorf("read smoke port: %w", err))
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return result, failure(3, fmt.Errorf("docker did not publish a loopback smoke port"))
	}
	result.URL = "http://" + net.JoinHostPort(host, port) + path
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, result.URL, nil)
		if err != nil {
			return result, failure(3, err)
		}
		response, err := client.Do(request)
		if err == nil {
			result.StatusCode = response.StatusCode
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if response.StatusCode >= 200 && (response.StatusCode < 400 || (p.HealthPath == "" && response.StatusCode < 500)) {
				return result, nil
			}
		}
		running, err := docker(ctx, dir, "inspect", "--format", "{{.State.Running}}", name)
		if err != nil || running != "true" {
			return result, failure(3, fmt.Errorf("smoke container stopped; check the app start command and .env"))
		}
		select {
		case <-ctx.Done():
			return result, failure(3, fmt.Errorf("HTTP smoke failed at %s (last status %d): %w", result.URL, result.StatusCode, ctx.Err()))
		case <-ticker.C:
		}
	}
}
