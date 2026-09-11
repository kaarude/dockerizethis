package node_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/templates/node"
	"github.com/stretchr/testify/require"
)

var dockerTests = flag.Bool("docker", false, "build and run generated Node Dockerfiles (requires Docker and registry access)")

// TestDocker exercises the generated artifacts without publishing host ports.
// Run explicitly with go test ./internal/templates/node -docker -run TestDocker -v.
func TestDocker(t *testing.T) {
	if !*dockerTests {
		t.Skip("use -docker to build and run generated images")
	}
	for _, tc := range []struct {
		name, manager, lockfile, lock string
		process                       plan.ProcessType
		distroless, native            bool
	}{
		{"web", "npm", "package-lock.json", npmLock, plan.ProcessWeb, false, false},
		{"worker", "pnpm", "pnpm-lock.yaml", pnpmLock, plan.ProcessWorker, false, true},
		{"static", "yarn", "yarn.lock", yarnLock, plan.ProcessStatic, false, false},
		{"bun", "bun", "bun.lock", bunLock, plan.ProcessWeb, false, false},
		{"distroless", "npm", "package-lock.json", npmLock, plan.ProcessWeb, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := webPlan()
			p.Process, p.PkgManager = tc.process, tc.manager
			p.Extras["lockfile"] = tc.lockfile
			p.Port = 8080
			p.BuildCmd, p.StartCmd = tc.manager+" run build", tc.manager+" run start"
			if tc.distroless {
				p.Extras["distroless"] = "true"
				p.StartCmd = "node dist/server.js"
			}
			if tc.native {
				p.Extras["nativeDeps"] = "better-sqlite3"
			}
			files, err := node.RenderNode(p)
			require.NoError(t, err)
			dir := t.TempDir()
			for _, file := range files {
				require.NoError(t, os.WriteFile(filepath.Join(dir, file.Path), file.Content, file.Mode))
			}
			manifest := fixturePackage
			// Bun deletes empty lockfiles, so use one small dependency to exercise frozen installs.
			if tc.manager == "bun" {
				manifest = strings.TrimSuffix(manifest, "}") + `,"dependencies":{"is-number":"7.0.0"}}`
			}
			if tc.manager == "yarn" {
				manifest = strings.TrimSuffix(manifest, "}") + `,"packageManager":"yarn@4.18.0"}`
			}
			for name, content := range map[string]string{
				"package.json": manifest,
				tc.lockfile:    tc.lock,
				"build.js":     `const fs = require('node:fs'); fs.mkdirSync('dist', {recursive:true}); fs.copyFileSync('server.js','dist/server.js'); fs.writeFileSync('dist/index.html','node-template-ok');`,
				"server.js":    `require('node:http').createServer((req,res)=>{res.statusCode=req.url==='/health'?200:503;res.end('node-template-ok')}).listen(8080,'0.0.0.0');`,
			} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
			}
			tag := fmt.Sprintf("dockerizethis-node-test:%s-%d", tc.name, time.Now().UnixNano())
			docker(t, "build", "--progress=plain", "-t", tag, dir)
			t.Cleanup(func() { docker(t, "image", "rm", tag) })
			name := "dockerizethis-node-" + tc.name + fmt.Sprint(time.Now().UnixNano())
			docker(t, "run", "-d", "--name", name, tag)
			t.Cleanup(func() { docker(t, "rm", "-f", name) })
			var configs []struct {
				Config struct {
					User         string
					WorkingDir   string
					Env          []string
					Healthcheck  *struct{ Test []string }
					ExposedPorts map[string]json.RawMessage
				}
			}
			require.NoError(t, json.Unmarshal([]byte(docker(t, "inspect", name)), &configs))
			config := configs[0].Config
			require.Contains(t, []string{"node", "1001"}, config.User)
			require.Equal(t, "/app", config.WorkingDir)
			require.Contains(t, config.Env, "NODE_ENV=production")
			if tc.process == plan.ProcessStatic {
				docker(t, "exec", name, "nginx", "-t")
				require.Equal(t, "node-template-ok", strings.TrimSpace(docker(t, "exec", name, "wget", "-qO-", "http://127.0.0.1:8080/")))
				return
			}
			nodeBin := "node"
			if tc.distroless {
				nodeBin = "/nodejs/bin/node"
			}
			script := `if(process.getuid()===0||process.env.NODE_ENV!=='production'||process.cwd()!=='/app'||require('node:fs').existsSync('/usr/bin/g++'))process.exit(1)`
			docker(t, "exec", name, nodeBin, "-e", script)
			if tc.process == plan.ProcessWorker {
				require.Nil(t, config.Healthcheck)
				require.Empty(t, config.ExposedPorts)
				waitForDockerExec(t, name, nodeBin, "-e", `fetch('http://127.0.0.1:8080/health').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))`)
				return
			}
			require.NotNil(t, config.Healthcheck)
			require.Contains(t, config.ExposedPorts, "8080/tcp")
			health := config.Healthcheck.Test
			require.Equal(t, "CMD", health[0])
			args := append([]string{"exec", name}, health[1:]...)
			waitForDockerExec(t, args[1:]...)
			// Prove the exact rendered healthcheck fails on non-success HTTP status.
			args[len(args)-1] = strings.ReplaceAll(args[len(args)-1], "/health", "/unhealthy")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr, "%s", output)
			require.Equal(t, 1, exitErr.ExitCode())
			if tc.manager == "bun" {
				docker(t, "exec", name, "node", "-e", `const fs=require('node:fs');const p=JSON.parse(fs.readFileSync('package.json'));p.dependencies['is-number']='6.0.0';fs.writeFileSync('package.json',JSON.stringify(p));`)
				output, err := exec.CommandContext(ctx, "docker", "exec", name, "bun", "i", "--frozen-lockfile").CombinedOutput()
				require.ErrorAs(t, err, &exitErr, "%s", output)
				require.Equal(t, 1, exitErr.ExitCode())
				require.Contains(t, string(output), "lockfile is frozen")
			}
		})
	}
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	require.NoError(t, err, "docker %v\n%s", args, output)
	return string(output)
}

func waitForDockerExec(t *testing.T, args ...string) {
	t.Helper()
	var output []byte
	var err error
	ready := false
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		output, err = exec.CommandContext(ctx, "docker", append([]string{"exec"}, args...)...).CombinedOutput()
		if err == nil {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		logs, _ := exec.Command("docker", "logs", args[0]).CombinedOutput()
		t.Fatalf("container did not become ready: %v\n%s\n%s", err, output, logs)
	}
}

const fixturePackage = `{"name":"node-template-fixture","version":"1.0.0","scripts":{"build":"node build.js","start":"node dist/server.js"}}`
const npmLock = `{"name":"node-template-fixture","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"node-template-fixture","version":"1.0.0"}}}`
const pnpmLock = "lockfileVersion: '9.0'\nsettings:\n  autoInstallPeers: true\n  excludeLinksFromLockfile: false\nimporters:\n  .: {}\n"
const yarnLock = `# This file is generated by running "yarn install" inside your project.
# Manual changes might be lost - proceed with caution!

__metadata:
  version: 10
  cacheKey: 10c0

"node-template-fixture@workspace:.":
  version: 0.0.0-use.local
  resolution: "node-template-fixture@workspace:."
  languageName: unknown
  linkType: soft
`
const bunLock = `{
  "lockfileVersion": 2,
  "configVersion": 1,
  "workspaces": {
    "": {
      "name": "node-template-fixture",
      "dependencies": {
        "is-number": "7.0.0",
      },
    },
  },
  "packages": {
    "is-number": ["is-number@7.0.0", "", {}, "sha512-41Cifkg6e8TylSpdtTpeLVMqvSBEVzTttHvERD741+pnZ8ANv0004MRL43QKPDlK9cGvNp6NZWZUBlbGXYxxng=="],
  }
}
`
