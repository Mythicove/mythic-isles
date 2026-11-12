// Build the unmodified official companion plugin at a fixed source revision.
import { mkdtemp, mkdir, copyFile, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import assert from "node:assert/strict";

const destination = process.argv[2];
if (!destination) throw new Error("Usage: node test/fetch-dynmap-worldguard.mjs /absolute/fixture-directory");
const source = await mkdtemp(join(tmpdir(), "dynmap-worldguard-source-"));
const revision = "82bdc16eec68bad16e14331d5588088551b22cbd";
const run = (command, args) => {
  const result = spawnSync(command, args, { cwd: source, encoding: "utf8", timeout: 600_000, windowsVerbatimArguments: command === "cmd.exe" });
  if (result.status !== 0) throw new Error(`${command} failed: ${result.stdout}\n${result.stderr}`);
  return result.stdout.trim();
};
run("git", ["clone", "https://github.com/webbukkit/Dynmap-WorldGuard.git", "."]);
run("git", ["checkout", "--detach", revision]);
assert.equal(run("git", ["rev-parse", "HEAD"]), revision);
const maven = process.env.MAVEN_COMMAND || (process.platform === "win32" ? "mvn.cmd" : "mvn");
if (process.platform === "win32") run("cmd.exe", ["/d", "/s", "/c", `""${maven}" -q package"`]);
else run(maven, ["-q", "package"]);
const artifact = join(source, "target", "Dynmap-WorldGuard-1.4-SNAPSHOT.jar");
const bytes = await readFile(artifact);
const dir = resolve(destination);
await mkdir(dir, { recursive: true });
await copyFile(artifact, join(dir, "Dynmap-WorldGuard.jar"));
const manifest = { version: "1.4-SNAPSHOT", revision, source, sha256: createHash("sha256").update(bytes).digest("hex") };
await writeFile(join(dir, "dynmap-worldguard-fixture.json"), JSON.stringify(manifest, null, 2));
console.log(JSON.stringify(manifest));
