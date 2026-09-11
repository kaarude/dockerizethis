# Python detection

`python.Detector{}` implements `detect.Detector`. It scans manifests and Python
source without importing or executing the project. Virtualenvs, tests, source
symlinks, comments, and multiline strings are excluded from source evidence.

Lockfile precedence is poetry, uv, then pdm; the fallback is pip. Version
precedence is .python-version, a supported numeric requires-python lower bound,
then 3.12. Dependency declarations and imports identify frameworks and services.
Web processes use port 8000; workers have no port.

Entry selection prefers main.py, app.py, and server.py, including src layouts.
Application constructor assignments identify ASGI/Flask objects; Django uses
wsgi.py. Workers use a package's __main__.py or a script. Guessed entries receive
confidence 0.5 and a note. A missing uvicorn/gunicorn dependency also gets a note.
A `/health` or `/healthz` route becomes the plan's health path for web
processes; `get`, `route`, and `add_url_rule` decorators and Django `path` and
`re_path` entries are recognized, with or without a trailing slash.

The scans are heuristics, not a complete TOML or Python parser. Dynamically
computed dependencies, app factories, aliases, version constraints without a
supported lower bound, and multiple applications require review. Environment
Required flags use the same name-based convention as Node, plus DJANGO_SECRET_KEY.
