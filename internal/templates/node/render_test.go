package node_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/templates/node"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update Node template golden files")

func webPlan() plan.Plan {
	return plan.Plan{
		Stack: "node", Version: "22", PkgManager: "npm", Process: plan.ProcessWeb,
		Port: 3000, HealthPath: "/health", BuildCmd: "npm run build", StartCmd: "node dist/server.js",
		Workdir: ".", Extras: map[string]string{},
	}
}

func TestRenderNodeGolden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*plan.Plan)
	}{
		{"web", func(p *plan.Plan) {}},
		{"worker", func(p *plan.Plan) {
			p.Process, p.PkgManager, p.Port = plan.ProcessWorker, "pnpm", 0
			p.BuildCmd, p.StartCmd = "pnpm run build", "pnpm start"
			p.Extras["nativeDeps"] = "better-sqlite3,bcrypt,sharp"
		}},
		{"static", func(p *plan.Plan) {
			p.Process, p.PkgManager, p.Port = plan.ProcessStatic, "yarn", 8080
			p.BuildCmd, p.StartCmd = "yarn build", ""
		}},
		{"bun", func(p *plan.Plan) {
			p.PkgManager, p.BuildCmd, p.StartCmd, p.HealthPath = "bun", "", "bun run start", ""
			p.Extras["lockfile"] = "bun.lockb"
		}},
		{"distroless-web", func(p *plan.Plan) { p.Extras["distroless"] = "true" }},
		{"distroless-worker", func(p *plan.Plan) {
			p.Extras["distroless"] = "true"
			p.Process, p.BuildCmd, p.StartCmd = plan.ProcessWorker, "", "node worker.js --queue=jobs"
		}},
		{"native-fallback", func(p *plan.Plan) {
			p.Extras["distroless"], p.Extras["nativeDeps"] = "true", "sharp"
			p.HealthPath = ""
		}},
		{"static-prebuilt", func(p *plan.Plan) {
			p.Process, p.BuildCmd, p.StartCmd, p.Port = plan.ProcessStatic, "", "", 8080
			p.Extras["staticDir"] = "public/site"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := node.RenderNode(p)
			require.NoError(t, err)
			var output bytes.Buffer
			for _, file := range files {
				fmt.Fprintf(&output, "--- %s (mode %04o) ---\n%s", file.Path, file.Mode, file.Content)
			}
			for _, note := range node.RenderNotes(p) {
				fmt.Fprintf(&output, "--- note ---\n%s\n", note)
			}
			golden := filepath.Join("..", "..", "..", "testdata", "golden", "node-"+tc.name+".golden")
			if *update {
				require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
				require.NoError(t, os.WriteFile(golden, output.Bytes(), 0o644))
			}
			want, err := os.ReadFile(golden)
			require.NoError(t, err)
			require.Equal(t, string(want), output.String(), "refresh with go test ./internal/templates/node -update")
		})
	}
}

func TestPackageManagers(t *testing.T) {
	for _, tc := range []struct{ manager, lock, command string }{
		{"npm", "package-lock.json", "npm ci"},
		{"npm", "npm-shrinkwrap.json", "npm ci"},
		{"pnpm", "pnpm-lock.yaml", "pnpm i --frozen-lockfile"},
		{"yarn", "yarn.lock", `sh -c 'case "$(yarn --version)" in 1.*) yarn --frozen-lockfile ;; *) yarn --immutable ;; esac'`},
		{"bun", "bun.lock", "bun i --frozen-lockfile"},
		{"bun", "bun.lockb", "bun i --frozen-lockfile"},
	} {
		t.Run(tc.lock, func(t *testing.T) {
			p := webPlan()
			p.PkgManager, p.Extras["lockfile"] = tc.manager, tc.lock
			files, err := node.RenderNode(p)
			require.NoError(t, err)
			dockerfile := artifact(t, files, "Dockerfile")
			require.Contains(t, dockerfile, "COPY package.json "+tc.lock+" ./\n")
			require.Contains(t, dockerfile, "RUN NODE_ENV=development "+tc.command+"\n")
		})
	}
}

func TestRenderNodeBehavior(t *testing.T) {
	for _, process := range []plan.ProcessType{plan.ProcessWeb, plan.ProcessWorker, plan.ProcessStatic} {
		for _, build := range []string{"", "npm run build"} {
			t.Run(string(process)+"/"+build, func(t *testing.T) {
				p := webPlan()
				p.Process, p.BuildCmd = process, build
				files, err := node.RenderNode(p)
				require.NoError(t, err)
				wantPaths := []string{"Dockerfile", ".dockerignore"}
				if process == plan.ProcessStatic {
					wantPaths = append(wantPaths, "nginx.conf")
				}
				var paths []string
				for _, file := range files {
					paths = append(paths, file.Path)
					require.EqualValues(t, 0o644, file.Mode)
				}
				require.Equal(t, wantPaths, paths)
				dockerfile := artifact(t, files, "Dockerfile")
				runtime := strings.Split(dockerfile, " AS runtime\n")[1]
				require.Contains(t, runtime, "WORKDIR /app\nENV NODE_ENV=production\n")
				require.NotContains(t, runtime, "apk add")
				if process == plan.ProcessWorker {
					require.NotContains(t, dockerfile, "EXPOSE")
					require.NotContains(t, dockerfile, "HEALTHCHECK")
				}
				ignore := artifact(t, files, ".dockerignore")
				require.Equal(t, build != "", strings.Contains(ignore, "\ndist\n"))
				for _, pattern := range []string{"node_modules", ".git", ".env*", "*.md", "Dockerfile", "docker-compose.yml"} {
					require.Contains(t, strings.Split(ignore, "\n"), pattern)
				}
				require.Equal(t, build != "", strings.Contains(dockerfile, "RUN npm run build\n"))
			})
		}
	}
}

func TestRenderNotes(t *testing.T) {
	p := webPlan()
	p.HealthPath, p.Notes = "", []string{"existing note"}
	before, err := json.Marshal(p)
	require.NoError(t, err)
	files, err := node.RenderNode(p)
	require.NoError(t, err)
	notes := node.RenderNotes(p)
	require.Len(t, notes, 1)
	require.Contains(t, artifact(t, files, "Dockerfile"), "# "+notes[0]+"\n")
	require.NotContains(t, artifact(t, files, "Dockerfile"), "HEALTHCHECK")
	after, err := json.Marshal(p)
	require.NoError(t, err)
	require.Equal(t, before, after, "rendering must not mutate the caller's plan")
	p.Notes = append(p.Notes, notes...)
	require.Equal(t, notes[0], p.Notes[1])
	p.Process = plan.ProcessWorker
	require.Empty(t, node.RenderNotes(p))
}

func TestHealthcheckEscaping(t *testing.T) {
	p := webPlan()
	p.HealthPath = `/health?value="quoted"&other=$HOME`
	p.StartCmd = `node server.js --name="a b" && echo '$done'`
	files, err := node.RenderNode(p)
	require.NoError(t, err)
	for _, line := range strings.Split(artifact(t, files, "Dockerfile"), "\n") {
		if strings.HasPrefix(line, "HEALTHCHECK ") {
			var args []string
			require.NoError(t, json.Unmarshal([]byte(strings.SplitN(line, " CMD ", 2)[1]), &args))
			require.Equal(t, []string{"node", "-e"}, args[:2])
			require.Contains(t, args[2], "process.exit(r.ok?0:1)")
			require.Contains(t, args[2], ".catch(()=>process.exit(1))")
			urlJSON := strings.SplitN(strings.TrimPrefix(args[2], "fetch("), ").then", 2)[0]
			var address string
			require.NoError(t, json.Unmarshal([]byte(urlJSON), &address))
			require.Equal(t, "http://127.0.0.1:3000"+p.HealthPath, address)
		}
		if strings.HasPrefix(line, "CMD ") {
			var args []string
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "CMD ")), &args))
			require.Equal(t, []string{"/bin/sh", "-c", p.StartCmd}, args)
		}
	}
}

func TestDistrolessCommands(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{`node dist/server.js --port=3000`, []string{"dist/server.js", "--port=3000"}},
		{`node "dist/my server.js" --name='a b'`, []string{"dist/my server.js", "--name=a b"}},
		{`node './worker'"'"'s file.js'`, []string{"./worker's file.js"}},
		{`node my\ file.js ""`, []string{"my file.js", ""}},
		{`node -e 'console.log("$HOME")'`, []string{"-e", `console.log("$HOME")`}},
		{`node "file\\name.js" "a\qb"`, []string{`file\name.js`, `a\qb`}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			p := webPlan()
			p.Extras["distroless"], p.StartCmd = "true", tc.command
			files, err := node.RenderNode(p)
			require.NoError(t, err)
			var got []string
			for _, line := range strings.Split(artifact(t, files, "Dockerfile"), "\n") {
				if strings.HasPrefix(line, "CMD ") {
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "CMD ")), &got))
				}
			}
			require.Equal(t, tc.want, got)
		})
	}
	for _, command := range []string{`node "$ENTRY"`, "node `pwd`/server.js", `node *.js`, `node server.js >log`, "node server.js \\", "node", `node ''`} {
		p := webPlan()
		p.Extras["distroless"], p.StartCmd = "true", command
		files, err := node.RenderNode(p)
		require.ErrorContains(t, err, "direct node command", command)
		require.Nil(t, files)
	}
}

func TestRenderNodeInvalidPlan(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*plan.Plan)
	}{
		{"stack", "stack", func(p *plan.Plan) { p.Stack = "go" }},
		{"version missing", "version", func(p *plan.Plan) { p.Version = "" }},
		{"version injection", "version", func(p *plan.Plan) { p.Version = "22\nRUN bad" }},
		{"process", "process", func(p *plan.Plan) { p.Process = "unknown" }},
		{"manager", "package manager", func(p *plan.Plan) { p.PkgManager = "unknown" }},
		{"port missing", "port", func(p *plan.Plan) { p.Port = 0 }},
		{"port too large", "port", func(p *plan.Plan) { p.Port = 65536 }},
		{"lock mismatch", "lockfile", func(p *plan.Plan) { p.Extras["lockfile"] = "yarn.lock" }},
		{"lock injection", "lockfile", func(p *plan.Plan) { p.Extras["lockfile"] = "package-lock.json\nRUN bad" }},
		{"build whitespace", "build command", func(p *plan.Plan) { p.BuildCmd = " " }},
		{"build injection", "build command", func(p *plan.Plan) { p.BuildCmd = "npm run build\nUSER root" }},
		{"build continuation", "build command", func(p *plan.Plan) { p.BuildCmd = "npm run build \\" }},
		{"start missing", "start command", func(p *plan.Plan) { p.StartCmd = "" }},
		{"start multiline", "start command", func(p *plan.Plan) { p.StartCmd = "node x\nUSER root" }},
		{"health remote", "health path", func(p *plan.Plan) { p.HealthPath = "https://example.com/health" }},
		{"health authority", "health path", func(p *plan.Plan) { p.HealthPath = "//example.com/health" }},
		{"health invalid", "health path", func(p *plan.Plan) { p.HealthPath = "/%zz" }},
		{"health newline", "health path", func(p *plan.Plan) { p.HealthPath = "/health\n" }},
		{"static traversal", "staticDir", func(p *plan.Plan) { p.Process = plan.ProcessStatic; p.Extras["staticDir"] = "../private" }},
		{"static root", "staticDir", func(p *plan.Plan) { p.Process = plan.ProcessStatic; p.Extras["staticDir"] = "." }},
		{"static injection", "staticDir", func(p *plan.Plan) { p.Process = plan.ProcessStatic; p.Extras["staticDir"] = "dist\nUSER root" }},
		{"distroless version", "major", func(p *plan.Plan) { p.Extras["distroless"] = "true"; p.Version = "22.1.0" }},
		{"distroless npm", "direct node command", func(p *plan.Plan) { p.Extras["distroless"] = "true"; p.StartCmd = "npm start" }},
		{"distroless shell", "direct node command", func(p *plan.Plan) { p.Extras["distroless"] = "true"; p.StartCmd = "node server.js && echo done" }},
		{"distroless quotes", "direct node command", func(p *plan.Plan) { p.Extras["distroless"] = "true"; p.StartCmd = `node "server.js` }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			tc.change(&p)
			files, err := node.RenderNode(p)
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, files, "invalid plans must not yield partial artifacts")
		})
	}
}

func artifact(t *testing.T, files []emit.File, name string) string {
	t.Helper()
	for _, file := range files {
		if file.Path == name {
			return string(file.Content)
		}
	}
	t.Fatalf("missing artifact %s", name)
	return ""
}
