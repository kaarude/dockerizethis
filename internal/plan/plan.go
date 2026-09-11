package plan

type ProcessType string
const (
    ProcessWeb    ProcessType = "web"
    ProcessWorker ProcessType = "worker"
    ProcessStatic ProcessType = "static"
)

type Service string
const (
    ServicePostgres Service = "postgres"
    ServiceRedis    Service = "redis"
    ServiceMySQL    Service = "mysql"
    ServiceMongo    Service = "mongo"
)

type EnvVar struct {
    Name     string `json:"name"`
    Required bool   `json:"required"`
    Hint     string `json:"hint,omitempty"`
}

type Plan struct {
    Stack      string            `json:"stack"`
    Version    string            `json:"version"`
    PkgManager string            `json:"pkgManager,omitempty"`
    Framework  string            `json:"framework,omitempty"`
    Process    ProcessType       `json:"process"`
    Port       int               `json:"port,omitempty"`
    HealthPath string            `json:"healthPath,omitempty"`
    Services   []Service         `json:"services,omitempty"`
    Env        []EnvVar          `json:"env,omitempty"`
    BuildCmd   string            `json:"buildCmd,omitempty"`
    StartCmd   string            `json:"startCmd"`
    Workdir    string            `json:"workdir"`
    Confidence float64           `json:"confidence"`
    Extras     map[string]string `json:"extras,omitempty"`
    Notes      []string          `json:"notes,omitempty"`
}
