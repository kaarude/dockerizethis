// Package verify builds and smoke-tests generated Docker artifacts.
//
// Build runs `docker build` and parses engine output — BuildKit
// "#NN ... ERROR" lines and legacy "Step X/Y" + "returned a non-zero code"
// failures — into a structured Result carrying the failed step, a tail of
// the build log, and a remediation hint.
//
// Smoke runs the built image for web-process plans and polls the plan's
// health endpoint. The container is started without the plan's env vars:
// their values are unknowable at verify time, so apps that cannot boot
// without them fail the smoke check. PORT alone is injected from
// plan.Port so the process binds the expected port.
//
// Exit-code contract: callers map the returned error to a process exit
// code with Code.
//
//	0  verification passed (err == nil)
//	1  usage or infrastructure error (bad arguments, context canceled, ...)
//	2  docker build failed
//	3  smoke test failed
//	4  docker CLI or daemon unavailable
package verify

import "errors"

// Sentinel errors wrapped by Build and Smoke failures. Use errors.Is to
// classify, or Code to map directly to a process exit code.
var (
	ErrDockerUnavailable = errors.New("verify: docker unavailable")
	ErrBuildFailed       = errors.New("verify: docker build failed")
	ErrSmokeFailed       = errors.New("verify: smoke test failed")
)

// Process exit codes returned by Code.
const (
	ExitOK                = 0
	ExitError             = 1
	ExitBuildFailed       = 2
	ExitSmokeFailed       = 3
	ExitDockerUnavailable = 4
)

// Code maps a verification error to the exit code contract documented on
// the package. A nil error is a pass; unrecognized errors map to 1.
func Code(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, ErrDockerUnavailable):
		return ExitDockerUnavailable
	case errors.Is(err, ErrBuildFailed):
		return ExitBuildFailed
	case errors.Is(err, ErrSmokeFailed):
		return ExitSmokeFailed
	default:
		return ExitError
	}
}
