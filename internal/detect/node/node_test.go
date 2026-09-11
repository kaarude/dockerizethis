package node_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/carl/dockerizethis/internal/detect/node"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/stretchr/testify/require"
)

func TestFixtures(t *testing.T) {
	for _, tc := range []struct {
		name string
		want plan.Plan
	}{
		{
			name: "node-express-pg",
			want: plan.Plan{
				Stack: "node", Version: "22", PkgManager: "npm", Framework: "express",
				Process: plan.ProcessWeb, Port: 8080, Services: []plan.Service{plan.ServicePostgres},
				Env:      []plan.EnvVar{{Name: "DATABASE_URL", Required: true}, {Name: "PORT"}},
				StartCmd: "node src/index.js", Workdir: "/app", Confidence: 1,
			},
		},
		{
			name: "node-discord-worker",
			want: plan.Plan{
				Stack: "node", Version: "20", PkgManager: "npm", Process: plan.ProcessWorker,
				Env:      []plan.EnvVar{{Name: "DISCORD_TOKEN", Required: true}},
				StartCmd: "node worker.mjs", Workdir: "/app", Confidence: 0.9,
			},
		},
		{
			name: "node-next",
			want: plan.Plan{
				Stack: "node", Version: "20", PkgManager: "npm", Framework: "next",
				Process: plan.ProcessWeb, Port: 3000, BuildCmd: "npm run build",
				StartCmd: "next start", Workdir: "/app", Confidence: 0.8,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := (node.Detector{}).Detect(filepath.Join("../../../testdata/fixtures", tc.name))
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, got)
		})
	}
	require.Equal(t, "node", (node.Detector{}).Name())
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func detectFiles(t *testing.T, files map[string]string) plan.Plan {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, dir, name, content)
	}
	p, ok, err := (node.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	return p
}

func TestMissingAndMalformedManifest(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		p, ok, err := (node.Detector{}).Detect(t.TempDir())
		require.NoError(t, err)
		require.False(t, ok)
		require.Equal(t, plan.Plan{}, p)
	})
	for _, contents := range []string{"", "{", "null", "[]", `{"scripts":{"start":123}}`, `{} {}`} {
		t.Run(contents, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "package.json", contents)
			p, ok, err := (node.Detector{}).Detect(dir)
			require.ErrorContains(t, err, "parse package.json")
			require.False(t, ok)
			require.Equal(t, plan.Plan{}, p)
		})
	}
	t.Run("manifest is directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "package.json"), 0o755))
		_, ok, err := (node.Detector{}).Detect(dir)
		require.ErrorContains(t, err, "read package.json")
		require.False(t, ok)
	})
}

func TestPackageManagers(t *testing.T) {
	locks := []string{"pnpm-lock.yaml", "yarn.lock", "bun.lockb", "package-lock.json"}
	for i, manager := range []string{"pnpm", "yarn", "bun", "npm", "npm"} {
		t.Run(manager+string(rune('0'+i)), func(t *testing.T) {
			files := map[string]string{"package.json": "{}"}
			for _, lock := range locks[i:] {
				files[lock] = ""
			}
			p := detectFiles(t, files)
			require.Equal(t, manager, p.PkgManager)
			if i < len(locks) {
				require.Equal(t, 0.6, p.Confidence)
			} else {
				require.Equal(t, 0.5, p.Confidence)
			}
		})
	}
	t.Run("directory is not a lockfile", func(t *testing.T) {
		p := detectFiles(t, map[string]string{"package.json": "{}", "pnpm-lock.yaml/entry": ""})
		require.Equal(t, "npm", p.PkgManager)
	})
}

func TestAlternativeLockfiles(t *testing.T) {
	for _, tc := range []struct {
		name, manager, selected string
		locks                   []string
	}{
		{"Bun text", "bun", "bun.lock", []string{"bun.lock"}},
		{"Bun text preferred", "bun", "bun.lock", []string{"bun.lock", "bun.lockb", "package-lock.json"}},
		{"npm shrinkwrap", "npm", "npm-shrinkwrap.json", []string{"npm-shrinkwrap.json"}},
		{"npm shrinkwrap preferred", "npm", "npm-shrinkwrap.json", []string{"npm-shrinkwrap.json", "package-lock.json"}},
		{"pnpm remains preferred", "pnpm", "", []string{"pnpm-lock.yaml", "bun.lock", "npm-shrinkwrap.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"package.json": "{}"}
			for _, lock := range tc.locks {
				files[lock] = ""
			}
			p := detectFiles(t, files)
			require.Equal(t, tc.manager, p.PkgManager)
			require.Equal(t, tc.selected, p.Extras["lockfile"])
			require.Equal(t, 0.6, p.Confidence)
		})
	}
}

func TestVersions(t *testing.T) {
	for _, tc := range []struct {
		name, engine, nvm, versionFile, want string
	}{
		{"engines wins", ">=22", "20", "18", "22"},
		{"range", ">=20.19.0 <23", "", "", "20.19.0"},
		{"caret", "^22.1.0", "", "", "22.1.0"},
		{"tilde", "~22.1.0", "", "", "22.1.0"},
		{"wildcard", "22.x", "", "", "22"},
		{"nvm wins", "", " v22.5.1\n", "18", "22.5.1"},
		{"version file", "", "", "22\n", "22"},
		{"blank nvm", "", " \n", "22", "22"},
		{"unsupported engine", "<22", "20", "", "20"},
		{"unsupported alias", "*", "lts/*", "22", "22"},
		{"default", "", "", "", "20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := json.Marshal(map[string]any{"engines": map[string]string{"node": tc.engine}})
			require.NoError(t, err)
			p := detectFiles(t, map[string]string{"package.json": string(manifest), ".nvmrc": tc.nvm, ".node-version": tc.versionFile})
			require.Equal(t, tc.want, p.Version)
		})
	}
	t.Run("unreadable version file", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "package.json", "{}")
		require.NoError(t, os.Mkdir(filepath.Join(dir, ".nvmrc"), 0o755))
		_, ok, err := (node.Detector{}).Detect(dir)
		require.False(t, ok)
		require.ErrorContains(t, err, "read .nvmrc")
	})
}

func TestProcessesAndFrameworks(t *testing.T) {
	for _, tc := range []struct {
		dependency, start, build, framework string
		process                             plan.ProcessType
		port                                int
	}{
		{"next", "next start", "", "next", plan.ProcessWeb, 3000},
		{"nuxt", "nuxt start", "", "nuxt", plan.ProcessWeb, 3000},
		{"express", "node server.js", "", "express", plan.ProcessWeb, 0},
		{"fastify", "node server.js", "", "fastify", plan.ProcessWeb, 0},
		{"koa", "node server.js", "", "koa", plan.ProcessWeb, 0},
		{"hono", "node server.js", "", "hono", plan.ProcessWeb, 0},
		{"discord.js", "", "", "", plan.ProcessWorker, 0},
		{"telegraf", "node bot.js", "", "", plan.ProcessWorker, 0},
		{"bullmq", "node queue.js", "", "", plan.ProcessWorker, 0},
		{"vite", "vite preview", "vite build", "", plan.ProcessStatic, 4173},
		{"react-scripts", "react-scripts start", "react-scripts build", "", plan.ProcessStatic, 8080},
		{"astro", "astro preview", "astro build", "astro", plan.ProcessStatic, 8080},
		{"astro", "", "", "astro", "", 0},
		{"express", "", "", "express", "", 0},
		{"", "node worker.js", "", "", plan.ProcessWorker, 0},
		{"", "node worker.js --queue emails", "", "", plan.ProcessWorker, 0},
		{"", "NODE_ENV=production tsx worker.ts", "", "", plan.ProcessWorker, 0},
		{"", "echo done", "", "", "", 0},
		{"", "node -e 'console.log(1)'", "", "", "", 0},
		{"", "node --test worker.js", "", "", "", 0},
		{"", "node --eval=code worker.js", "", "", "", 0},
	} {
		t.Run(tc.dependency+"/"+tc.start, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{
				"dependencies": map[string]string{tc.dependency: "*"},
				"scripts":      map[string]string{"start": tc.start, "build": tc.build},
			})
			require.NoError(t, err)
			p := detectFiles(t, map[string]string{"package.json": string(data)})
			require.Equal(t, tc.framework, p.Framework)
			require.Equal(t, tc.process, p.Process)
			require.Equal(t, tc.port, p.Port)
			require.Equal(t, tc.start, p.StartCmd)
			if tc.build != "" {
				require.Equal(t, "npm run build", p.BuildCmd)
			} else {
				require.Empty(t, p.BuildCmd)
			}
		})
	}
	t.Run("server takes precedence", func(t *testing.T) {
		p := detectFiles(t, map[string]string{"package.json": `{"dependencies":{"astro":"*","express":"*","bullmq":"*"},"scripts":{"start":"node server.js","build":"astro build"}}`})
		require.Equal(t, "express", p.Framework)
		require.Equal(t, plan.ProcessWeb, p.Process)
	})
	t.Run("dev dependencies count", func(t *testing.T) {
		p := detectFiles(t, map[string]string{"package.json": `{"devDependencies":{"vite":"*"},"scripts":{"build":"vite build"}}`})
		require.Equal(t, plan.ProcessStatic, p.Process)
	})
	t.Run("react-scripts builds to build", func(t *testing.T) {
		p := detectFiles(t, map[string]string{"package.json": `{"dependencies":{"react-scripts":"*"},"scripts":{"start":"react-scripts start","build":"react-scripts build"}}`})
		require.Equal(t, "build", p.Extras["staticDir"])
	})
	t.Run("vite config wins over react-scripts", func(t *testing.T) {
		p := detectFiles(t, map[string]string{"package.json": `{"dependencies":{"react-scripts":"*"},"devDependencies":{"vite":"*"},"scripts":{"build":"vite build"}}`})
		require.Equal(t, plan.ProcessStatic, p.Process)
		require.Equal(t, 4173, p.Port)
		require.Empty(t, p.Extras["staticDir"])
	})
	t.Run("worker with build tooling has no preview port", func(t *testing.T) {
		p := detectFiles(t, map[string]string{"package.json": `{"dependencies":{"bullmq":"*"},"devDependencies":{"vite":"*"},"scripts":{"start":"node worker.js","build":"vite build"}}`})
		require.Equal(t, plan.ProcessWorker, p.Process)
		require.Zero(t, p.Port)
	})
}

func TestStartFallback(t *testing.T) {
	p := detectFiles(t, map[string]string{"package.json": `{"main":"worker's file.js"}`})
	require.Equal(t, `node './worker'"'"'s file.js'`, p.StartCmd)
	p = detectFiles(t, map[string]string{"package.json": `{"main":"-worker.js"}`})
	require.Equal(t, "node './-worker.js'", p.StartCmd)
	require.Equal(t, plan.ProcessWorker, p.Process)
	p = detectFiles(t, map[string]string{"package.json": `{"main":"ignored.js","scripts":{"start":"node chosen.js"}}`})
	require.Equal(t, "node chosen.js", p.StartCmd)
	p = detectFiles(t, map[string]string{"package.json": "{}"})
	require.Empty(t, p.StartCmd)
	require.Empty(t, p.Process)
	require.Equal(t, 0.5, p.Confidence)
	require.Contains(t, p.Notes, "no start script or main field; provide a start command")
}

func TestEnvironmentScan(t *testing.T) {
	p := detectFiles(t, map[string]string{
		"package.json":                 "{}",
		"src/index.ts":                 "process.env.DATABASE_URL; process.env.PORT; process.env.NODE_ENV; process.env.DATABASE_URL",
		"src/nested/module.mjs":        "process.env.DISCORD_TOKEN; process.env._PRIVATE; process.env.API2_KEY",
		"src/other.js":                 "process.env.REDIS_URL; process.env.lowercase; process.env['IGNORED']; process.env.UPPERlower",
		"node_modules/dependency/a.js": "process.env.IGNORED_DEP",
		"src/node_modules/a.ts":        "process.env.IGNORED_NESTED_DEP",
		"dist/a.js":                    "process.env.IGNORED_DIST",
		"build/a.ts":                   "process.env.IGNORED_BUILD",
		"src/build/a.js":               "process.env.IGNORED_NESTED_BUILD",
		".git/hook.js":                 "process.env.IGNORED_GIT",
		"readme.md":                    "process.env.IGNORED_MARKDOWN",
	})
	require.Equal(t, []plan.EnvVar{
		{Name: "API2_KEY"}, {Name: "DATABASE_URL", Required: true}, {Name: "DISCORD_TOKEN", Required: true},
		{Name: "NODE_ENV"}, {Name: "PORT"}, {Name: "REDIS_URL", Required: true}, {Name: "_PRIVATE"},
	}, p.Env)
	require.Zero(t, p.Port)
	t.Run("source symlinks are skipped", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "package.json", "{}")
		outside := t.TempDir()
		writeFile(t, outside, "source.js", "process.env.OUTSIDE")
		require.NoError(t, os.Symlink(filepath.Join(outside, "source.js"), filepath.Join(dir, "linked.js")))
		p, ok, err := (node.Detector{}).Detect(dir)
		require.NoError(t, err)
		require.True(t, ok)
		require.Empty(t, p.Env)
	})
}

func TestSourceReadFailures(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	for _, name := range []string{"unreadable.js", "src"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "package.json", "{}")
			writeFile(t, dir, "unreadable.js", "process.env.DATABASE_URL")
			writeFile(t, dir, "src/index.ts", "process.env.REDIS_URL")
			path := filepath.Join(dir, name)
			require.NoError(t, os.Chmod(path, 0))
			t.Cleanup(func() { require.NoError(t, os.Chmod(path, 0o755)) })
			p, ok, err := (node.Detector{}).Detect(dir)
			require.ErrorContains(t, err, "scan Node sources")
			require.False(t, ok)
			require.Equal(t, plan.Plan{}, p)
		})
	}
}

func TestPorts(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   int
	}{
		{"app.listen(process.env.PORT || 8080)", 8080},
		{"app.listen(Number(process.env.PORT) ?? 8081)", 8081},
		{"const port = parseInt(process.env.PORT || '8082', 10)", 8082},
		{"const port = parseInt(process.env.PORT, 10) || 8083", 8083},
		{"const port = process.env.PORT ?? 65535", 65535},
		{"const port = process.env.PORT || 65536", 3000},
		{"const port = process.env.PORT || 0", 3000},
		{"const port = process.env.PORT || -1", 3000},
		{"const port = process.env.PORT_NUMBER || 8080", 3000},
		{"app.listen(process.env.PORT)", 3000},
		{"app.listen(8080)", 3000},
	} {
		t.Run(tc.source, func(t *testing.T) {
			p := detectFiles(t, map[string]string{
				"package.json": `{"dependencies":{"next":"*"},"scripts":{"start":"next start"}}`,
				"server.js":    tc.source,
				"dist/out.js":  "process.env.PORT || 9000",
			})
			require.Equal(t, tc.want, p.Port)
		})
	}
}

func TestServices(t *testing.T) {
	for _, tc := range []struct {
		env, dependency string
		want            plan.Service
	}{
		{"DATABASE_URL", "pg", plan.ServicePostgres},
		{"REDIS_URL", "ioredis", plan.ServiceRedis},
		{"MONGO_URI", "mongoose", plan.ServiceMongo},
		{"MONGODB_URI", "mongoose", plan.ServiceMongo},
		{"MYSQL", "mysql2", plan.ServiceMySQL},
		{"MYSQL_HOST", "mysql2", plan.ServiceMySQL},
		{"DATABASE_URL", "mysql2", ""},
		{"REDIS_URL", "pg", ""},
		{"DATABASE_URL", "", ""},
		{"", "pg", ""},
	} {
		t.Run(tc.env+"/"+tc.dependency, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{"dependencies": map[string]string{tc.dependency: "*"}})
			require.NoError(t, err)
			p := detectFiles(t, map[string]string{"package.json": string(data), "db.ts": "process.env." + tc.env})
			if tc.want == "" {
				require.Empty(t, p.Services)
			} else {
				require.Equal(t, []plan.Service{tc.want}, p.Services)
				require.True(t, p.Env[0].Required)
			}
		})
	}
	t.Run("deduplicated and sorted", func(t *testing.T) {
		p := detectFiles(t, map[string]string{
			"package.json": `{"dependencies":{"pg":"*","mysql2":"*","ioredis":"*","mongoose":"*","prisma":"*"}}`,
			"db.ts":        "process.env.REDIS_URL; process.env.MYSQL_HOST; process.env.MYSQL_PASSWORD; process.env.DATABASE_URL; process.env.MONGO_URI; process.env.DATABASE_URL;",
		})
		require.Equal(t, []plan.Service{plan.ServiceMongo, plan.ServiceMySQL, plan.ServicePostgres, plan.ServiceRedis}, p.Services)
		require.Contains(t, p.Notes, "prisma detected — confirm database and run migrations outside the container")
	})
	for _, dependency := range []string{"prisma", "@prisma/client"} {
		t.Run(dependency, func(t *testing.T) {
			p := detectFiles(t, map[string]string{"package.json": `{"devDependencies":{"` + dependency + `":"*"}}`})
			require.Equal(t, []plan.Service{plan.ServicePostgres}, p.Services)
			require.Contains(t, p.Notes, "prisma detected — confirm database and run migrations outside the container")
		})
	}
}
