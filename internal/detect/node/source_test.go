package node_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeEvidenceIgnoresExamplesAndOtherServers(t *testing.T) {
	const app = "const express = require('express'); const app = express();\n"
	for _, tc := range []struct {
		name, source string
		port         int
		health       string
	}{
		{"commented example", "// app.listen(9000); app.get('/health', h);\napp.listen(3000);", 3000, ""},
		{"block comment", "/* app.listen(9000); app.get('/health', h); */\napp.listen(3000);", 3000, ""},
		{"string example", "const example = \"app.listen(9000); app.get('/health', h);\";\napp.listen(3000);", 3000, ""},
		{"template example", "const example = `app.listen(9000); app.get('/health', h);`;\napp.listen(3000);", 3000, ""},
		{"regex is ambiguous", "const pattern = /app.listen(9000)/;\napp.listen(3000);", 0, ""},
		{"template interpolation is ambiguous", "const example = `text ${ `app.listen(9000)` }`;\napp.listen(3000);", 0, ""},
		{"other server", "const net = require('net'); const socket = net.createServer();\nsocket.listen(9000);\napp.listen(3000);", 3000, ""},
		{"numeric expression", "app.listen(3000 + 1);", 0, ""},
		{"decimal port", "app.listen(3000.5);", 0, ""},
		{"multiple ports", "app.listen(3000); app.listen(4000);", 0, ""},
		{"nested factory", "function unused() { app.get('/health', h); app.listen(9000); }\napp.listen(3000);", 3000, ""},
		{"arrow factory", "const unused = () => app.listen(9000);\napp.listen(3000);", 3000, ""},
		{"middleware", "app.use('/health', h); app.all('/healthz', h); app.listen(3000);", 3000, ""},
		{"mounted router", "const router = express.Router(); router.get('/health', h); app.use('/api', router); app.listen(3000);", 3000, ""},
		{"strict slash route", "app.get('/health/', h); app.listen(3000);", 3000, "/health/"},
		{"wrong method", "app.post('/health', h); app.listen(3000);", 3000, ""},
		{"computed path", "app.get('/health' + '/private', h); app.listen(3000);", 3000, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := detectFiles(t, map[string]string{
				"package.json":      `{"dependencies":{"express":"*"},"scripts":{"start":"node server.js"}}`,
				"server.js":         app + tc.source,
				"aaa.test.js":       "app.listen(9001); app.get('/healthz', h);",
				"tests/index.js":    "app.listen(9002); app.get('/healthz', h);",
				"examples/index.js": "app.listen(9003); app.get('/healthz', h);",
				"unrelated.js":      "const app = require('express')(); app.listen(9004); app.get('/healthz', h);",
			})
			require.Equal(t, tc.port, p.Port)
			require.Equal(t, tc.health, p.HealthPath)
		})
	}
}

func TestRecognizedAppBindings(t *testing.T) {
	for _, tc := range []struct {
		name, framework, source string
		port                    int
		health                  string
	}{
		{"direct require", "express", "const app = require('express')(); app.get('/health', h); app.listen(3000);", 3000, "/health"},
		{"default import", "express", "import express from 'express'; const app = express(); app.get('/health', h); app.listen(3000);", 3000, "/health"},
		{"fastify", "fastify", "import fastify from 'fastify'; const app = fastify(); app.get('/health', h); app.listen(3000);", 3000, "/health"},
		{"koa", "koa", "import Koa from 'koa'; const app = new Koa(); app.listen(3000);", 3000, ""},
		{"ambiguous apps", "express", "const a = require('express')(); const b = require('express')(); a.listen(3000); b.listen(4000);", 0, ""},
		{"unknown factory", "express", "const app = makeApp(); app.get('/health', h); app.listen(3000);", 0, ""},
		{"chained factory", "express", "const app = require('express')().Router(); app.get('/health', h); app.listen(3000);", 0, ""},
		{"chained imported factory", "express", "import express from 'express'; const app = express().Router(); app.get('/health', h); app.listen(3000);", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := detectFiles(t, map[string]string{
				"package.json": `{"dependencies":{"` + tc.framework + `":"*"},"scripts":{"start":"node server.js"}}`,
				"server.js":    tc.source,
			})
			require.Equal(t, tc.port, p.Port)
			require.Equal(t, tc.health, p.HealthPath)
		})
	}
}

func TestNuxtHealthRoutes(t *testing.T) {
	for _, tc := range []struct{ file, want string }{
		{"server/api/health.get.ts", "/api/health"},
		{"server/api/health.ts", "/api/health"},
		{"server/routes/healthz.get.js", "/healthz"},
		{"server/routes/health/index.get.ts", "/health"},
		{"server/api/health.post.ts", ""},
		{"server/api/[tenant]/health.get.ts", ""},
		{"pages/health.vue", "/health"},
		{"app/pages/health.vue", "/health"},
		{"app/health/route.ts", ""},
		{"lib/server/api/health.get.ts", ""},
	} {
		t.Run(tc.file, func(t *testing.T) {
			p := detectFiles(t, map[string]string{
				"package.json": `{"dependencies":{"nuxt":"*"},"scripts":{"start":"node .output/server/index.mjs"}}`,
				tc.file:        "export default defineEventHandler(() => 'ok');",
			})
			require.Equal(t, tc.want, p.HealthPath)
		})
	}
}

func TestNextRouteRequiresGET(t *testing.T) {
	for _, source := range []string{
		"export function POST() { return new Response('ok'); }",
		"// export function GET() {}\nexport function POST() {}",
		"const example = 'export function GET() {}';",
	} {
		p := detectFiles(t, map[string]string{
			"package.json":        `{"dependencies":{"next":"*"},"scripts":{"start":"next start"}}`,
			"app/health/route.ts": source,
		})
		require.Empty(t, p.HealthPath)
	}
}

func TestNextExportsBeforeHandlerBody(t *testing.T) {
	for _, tc := range []struct{ file, source string }{
		{"app/health/page.tsx", "export default function Health() { return <div>ok</div>; }"},
		{"app/health/route.ts", "export function GET() { const pattern = /ok/; return new Response('ok'); }"},
	} {
		p := detectFiles(t, map[string]string{
			"package.json": `{"dependencies":{"next":"*"},"scripts":{"start":"next start"}}`,
			tc.file:        tc.source,
		})
		require.Equal(t, "/health", p.HealthPath)
	}
}
