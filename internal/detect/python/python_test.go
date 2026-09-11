package python_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/carl/dockerizethis/internal/detect/python"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/stretchr/testify/require"
)

func TestFixtures(t *testing.T) {
	for _, tc := range []struct {
		name string
		want plan.Plan
	}{
		{"py-fastapi-pg", plan.Plan{Stack: "python", Version: "3.12", PkgManager: "pip", Framework: "fastapi", Process: plan.ProcessWeb, Port: 8000, HealthPath: "/health", Services: []plan.Service{plan.ServicePostgres}, Env: []plan.EnvVar{{Name: "DATABASE_URL", Required: true}}, StartCmd: "uvicorn main:app --host 0.0.0.0 --port 8000", Workdir: "/app", Confidence: 0.9}},
		{"py-django", plan.Plan{Stack: "python", Version: "3.12", PkgManager: "pip", Framework: "django", Process: plan.ProcessWeb, Port: 8000, HealthPath: "/health", Env: []plan.EnvVar{{Name: "DJANGO_SECRET_KEY", Required: true}}, StartCmd: "gunicorn config.wsgi:application --bind 0.0.0.0:8000", Workdir: "/app", Confidence: 0.9}},
		{"py-discord-worker", plan.Plan{Stack: "python", Version: "3.12", PkgManager: "pip", Framework: "discord.py", Process: plan.ProcessWorker, Env: []plan.EnvVar{{Name: "DISCORD_TOKEN", Required: true}}, StartCmd: "python bot.py", Workdir: "/app", Confidence: 0.9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ok, err := (python.Detector{}).Detect(filepath.Join("..", "..", "..", "testdata", "fixtures", tc.name))
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, p)
		})
	}
	require.Equal(t, "python", (python.Detector{}).Name())
}

func TestManagersAndVersions(t *testing.T) {
	for _, manager := range []string{"pip", "poetry", "uv", "pdm"} {
		t.Run(manager, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "pyproject.toml", "[project]\nname = 'worker'\nrequires-python = '>=3.11,<3.13'\ndependencies = ['celery>=5', 'redis']\n")
			if manager != "pip" {
				write(t, dir, manager+".lock", "")
			}
			write(t, dir, "src/worker/__main__.py", "import os\nprint(os.getenv('QUEUE'))\n")
			p, ok, err := (python.Detector{}).Detect(dir)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, manager, p.PkgManager)
			require.Equal(t, "3.11", p.Version)
			require.Equal(t, "python -m worker", p.StartCmd)
			require.Equal(t, "/app/src", p.Extras["pythonpath"])
			require.Equal(t, plan.ProcessWorker, p.Process)
			require.Zero(t, p.Port)
			require.Equal(t, []plan.Service{plan.ServiceRedis}, p.Services)
			write(t, dir, ".python-version", "3.12.8\n")
			p, _, err = (python.Detector{}).Detect(dir)
			require.NoError(t, err)
			require.Equal(t, "3.12.8", p.Version)
		})
	}
	dir := t.TempDir()
	write(t, dir, "pyproject.toml", "[project]\nname = 'test'\n")
	for _, manager := range []string{"poetry", "uv", "pdm"} {
		write(t, dir, manager+".lock", "")
	}
	p, _, err := (python.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.Equal(t, "poetry", p.PkgManager)
}

func TestEntryLayouts(t *testing.T) {
	for _, tc := range []struct{ file, deps, code, want string }{
		{"app.py", "flask\ngunicorn", "from flask import Flask\napp = Flask(__name__)", "gunicorn app:app --bind 0.0.0.0:8000"},
		{"server.py", "starlette\nuvicorn", "from starlette.applications import Starlette\napp = Starlette()", "uvicorn server:app --host 0.0.0.0 --port 8000"},
		{"src/main.py", "fastapi\nuvicorn", "from fastapi import FastAPI\napp = FastAPI()", "uvicorn main:app --host 0.0.0.0 --port 8000"},
		{"src/service/server.py", "fastapi\nuvicorn", "import fastapi\napi = fastapi.FastAPI()", "uvicorn service.server:api --host 0.0.0.0 --port 8000"},
		{"worker/__main__.py", "dramatiq", "print('started')", "python -m worker"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "requirements.txt", tc.deps)
			write(t, dir, tc.file, tc.code)
			p, ok, err := (python.Detector{}).Detect(dir)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, p.StartCmd)
			require.Empty(t, p.Notes)
			require.Equal(t, 0.9, p.Confidence)
		})
	}
}

func TestScanAndGuesses(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "setup.py", "from setuptools import setup\nsetup(name='worker', install_requires=['dramatiq'])")
	write(t, dir, "worker.py", `import os
import asyncpg, redis as cache
from pymongo import MongoClient
import MySQLdb
# os.getenv("IGNORED_COMMENT")
"""os.getenv('IGNORED_DOCSTRING')"""
a = os.environ["DATABASE_URL"]
b = os.getenv('REDIS_URL')
e = os.environ.get("MONGO_URI")
c = os.getenv('DATABASE_URL')
d = os.getenv('OPTIONAL', 'default')
`)
	for _, name := range []string{"tests/test_app.py", ".venv/app.py", "venv/app.py", "env/app.py"} {
		write(t, dir, name, "import fastapi\nvalue = os.getenv('IGNORED')")
	}
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "missing.py"), filepath.Join(dir, "link.py")))
	p, ok, err := (python.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, plan.Plan{Stack: "python", Version: "3.12", PkgManager: "pip", Process: plan.ProcessWorker,
		Services: []plan.Service{plan.ServiceMongo, plan.ServiceMySQL, plan.ServicePostgres, plan.ServiceRedis},
		Env:      []plan.EnvVar{{Name: "DATABASE_URL", Required: true}, {Name: "MONGO_URI", Required: true}, {Name: "OPTIONAL"}, {Name: "REDIS_URL", Required: true}}, StartCmd: "python worker.py", Workdir: "/app", Confidence: 0.5, Extras: map[string]string{"pipSource": "setup.py"}, Notes: []string{"entry point guessed as worker.py; confirm the start command"}}, p)
	write(t, dir, "requirements.txt", "fastapi\ncelery\n")
	p, _, err = (python.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.Equal(t, plan.ProcessWeb, p.Process)
	require.Equal(t, 8000, p.Port)
	require.Less(t, p.Confidence, 0.9)
	require.Contains(t, p.Notes[0], "uvicorn is required")
}

func TestAbsentAndFailures(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "main.py", "import fastapi")
	p, ok, err := (python.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, plan.Plan{}, p)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "requirements.txt"), 0o755))
	_, ok, err = (python.Detector{}).Detect(dir)
	require.ErrorContains(t, err, "read requirements.txt")
	require.False(t, ok)
	dir = t.TempDir()
	write(t, dir, "requirements.txt", "")
	write(t, dir, "uv.lock", "")
	_, ok, err = (python.Detector{}).Detect(dir)
	require.ErrorContains(t, err, "requires pyproject.toml")
	require.False(t, ok)
	dir = t.TempDir()
	write(t, dir, "requirements.txt", "")
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".python-version"), 0o755))
	_, ok, err = (python.Detector{}).Detect(dir)
	require.ErrorContains(t, err, "read .python-version")
	require.False(t, ok)
}

func write(t *testing.T, dir, name, data string) {
	t.Helper()
	filename := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	require.NoError(t, os.WriteFile(filename, []byte(data), 0o644))
}

func TestDependencyDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name, file, text string
		framework        string
		services         []plan.Service
	}{
		{"multiline extras", "pyproject.toml", "[project]\nname = 'worker'\ndependencies = [\n  'psycopg[binary]>=3',\n  'redis',\n  'pymongo',\n]\n", "", []plan.Service{plan.ServiceMongo, plan.ServicePostgres, plan.ServiceRedis}},
		{"metadata and dev tools", "pyproject.toml", "[project]\nname = 'flask'\ndescription = 'fastapi'\n[tool.pytest.ini_options]\nmarkers = ['django']\n[tool.poetry.group.dev.dependencies]\nstarlette = '*'\n", "", nil},
		{"poetry dependencies", "pyproject.toml", "[tool.poetry.dependencies]\npython = '^3.12'\nfastapi = '^0.115'\nuvicorn = '*'\n", "fastapi", nil},
		{"setup extras", "setup.py", "setup(name='flask', install_requires=['psycopg[binary]', 'redis'])", "", []plan.Service{plan.ServicePostgres, plan.ServiceRedis}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, tc.file, tc.text)
			p, ok, err := (python.Detector{}).Detect(dir)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.framework, p.Framework)
			require.Equal(t, tc.services, p.Services)
		})
	}
}

func TestWorkerDependenciesTakePrecedenceWithoutWebFramework(t *testing.T) {
	for _, dependencies := range []string{"celery\nuvicorn\n", "dramatiq\ngunicorn\n", "discord.py\nuvicorn\n"} {
		dir := t.TempDir()
		write(t, dir, "requirements.txt", dependencies)
		p, ok, err := (python.Detector{}).Detect(dir)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, plan.ProcessWorker, p.Process)
		require.Zero(t, p.Port)
	}
}
