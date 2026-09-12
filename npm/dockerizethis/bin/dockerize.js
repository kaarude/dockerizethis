#!/usr/bin/env node
// Resolves the platform-specific dockerize binary installed via
// optionalDependencies and runs it with the caller's arguments.

const { spawnSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const PLATFORM_PACKAGES = {
  "darwin-arm64": "dockerizethis-darwin-arm64",
  "darwin-x64": "dockerizethis-darwin-x64",
  "linux-arm64": "dockerizethis-linux-arm64",
  "linux-x64": "dockerizethis-linux-x64",
  "win32-arm64": "dockerizethis-win32-arm64",
  "win32-x64": "dockerizethis-win32-x64",
};

const BINARY = process.platform === "win32" ? "dockerize.exe" : "dockerize";

function fromPlatformPackage() {
  const name = PLATFORM_PACKAGES[`${process.platform}-${process.arch}`];
  if (!name) {
    return null;
  }
  try {
    const manifest = require.resolve(`${name}/package.json`);
    const candidate = path.join(path.dirname(manifest), "bin", BINARY);
    return fs.existsSync(candidate) ? candidate : null;
  } catch {
    return null;
  }
}

function fromPath() {
  // A dockerize earlier on PATH (e.g. `go install`) works too — but never
  // resolve back to this shim or we would spawn ourselves forever.
  const self = fs.realpathSync(__filename);
  for (const dir of (process.env.PATH || "").split(path.delimiter)) {
    if (!dir) {
      continue;
    }
    const candidate = path.join(dir, BINARY);
    try {
      if (fs.realpathSync(candidate) === self) {
        continue;
      }
      const stat = fs.statSync(candidate);
      if (stat.isFile()) {
        return candidate;
      }
    } catch {
      // Not present or not accessible in this PATH entry.
    }
  }
  return null;
}

function main() {
  const binary =
    process.env.DOCKERIZE_BIN || fromPlatformPackage() || fromPath();
  if (!binary) {
    const key = `${process.platform}-${process.arch}`;
    console.error(
      `dockerizethis: no prebuilt binary for ${key}.\n` +
        "Install one with `go install github.com/carl/dockerizethis/cmd/dockerize@latest`\n" +
        "or set DOCKERIZE_BIN to an existing dockerize binary.",
    );
    process.exit(1);
  }

  const result = spawnSync(binary, process.argv.slice(2), {
    stdio: "inherit",
    env: process.env,
  });
  if (result.error) {
    console.error(`dockerizethis: failed to run ${binary}: ${result.error}`);
    process.exit(1);
  }
  if (result.signal) {
    process.kill(process.pid, result.signal);
    return;
  }
  process.exit(result.status ?? 1);
}

main();
