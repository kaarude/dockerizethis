const assert = require("node:assert/strict");
const { spawn, spawnSync } = require("node:child_process");
const { once } = require("node:events");
const path = require("node:path");
const { test } = require("node:test");

const launcher = path.join(__dirname, "../bin/dockerize.js");
const env = { ...process.env, DOCKERIZE_BIN: process.execPath };

for (const signal of ["SIGINT", "SIGTERM"]) {
  test(`forwards ${signal} and waits for cleanup`, {
    skip: process.platform === "win32",
    timeout: 5000,
  }, async (t) => {
    const child = spawn(process.execPath, [launcher, "-e", `
      process.on("${signal}", () => {
        setTimeout(() => {
          console.log("cleanup complete");
          process.exit(7);
        }, 100);
      });
      console.log(process.pid);
      setInterval(() => {}, 1000);
    `], { env });
    let binaryPID;
    t.after(() => {
      child.kill("SIGKILL");
      if (binaryPID) {
        try { process.kill(binaryPID, "SIGKILL"); } catch (error) {
          if (error.code !== "ESRCH") throw error;
        }
      }
    });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (data) => { stdout += data; });
    child.stderr.on("data", (data) => { stderr += data; });
    const closed = once(child, "close");
    await once(child.stdout, "data");
    binaryPID = Number(stdout.trim());
    assert.ok(Number.isInteger(binaryPID) && binaryPID > 0);
    child.kill(signal);
    const [code, exitSignal] = await closed;
    assert.equal(exitSignal, null, stderr);
    assert.equal(code, 7, stderr);
    assert.match(stdout, /cleanup complete/);
    assert.throws(() => process.kill(binaryPID, 0), { code: "ESRCH" });
    binaryPID = undefined;
  });
}

test("preserves arguments, output, and the child exit code", () => {
  const result = spawnSync(process.execPath, [launcher, "-e", `
    console.log(process.argv[1]);
    console.error("child diagnostic");
    process.exit(3);
  `, "argument with spaces"], { env, encoding: "utf8", timeout: 5000 });
  assert.equal(result.status, 3);
  assert.equal(result.stdout, "argument with spaces\n");
  assert.equal(result.stderr, "child diagnostic\n");
});

test("preserves a signal that terminates the child", {
  skip: process.platform === "win32",
}, () => {
  const result = spawnSync(process.execPath, [launcher, "-e",
    'process.kill(process.pid, "SIGTERM")',
  ], { env, encoding: "utf8", timeout: 5000 });
  assert.equal(result.error, undefined);
  assert.equal(result.signal, "SIGTERM");
});

test("reports a failure to start the binary", () => {
  const result = spawnSync(process.execPath, [launcher], {
    env: { ...env, DOCKERIZE_BIN: path.join(__dirname, "missing-binary") },
    encoding: "utf8",
    timeout: 5000,
  });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /failed to run .*missing-binary.*ENOENT/);
});
