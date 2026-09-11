package detect

import "github.com/carl/dockerizethis/internal/plan"

// Detector returns ok=false when its stack is absent from dir.
type Detector interface {
    Name() string
    Detect(dir string) (p plan.Plan, ok bool, err error)
}
