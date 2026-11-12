// Compile the official fixed source: the Maven API JAR does not enable as a plugin.
// Requires Maven and JDK 25 (set JAVA_HOME for this process only).
import { mkdtemp, mkdir, copyFile, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import assert from "node:assert/strict";
const destination = process.argv[2];
if (!destination) throw new Error("Usage: node test/fetch-coreprotect.mjs /absolute/fixture-directory");
const source = await mkdtemp(join(tmpdir(), "coreprotect-source-"));
const revision = "0af209a0a05135216599113c0b5e2638ad3c704b";
const run = (command, args) => {
  const result = spawnSync(command, args, { cwd: source, encoding: "utf8", timeout: 600_000, windowsVerbatimArguments: command === "cmd.exe" });
  if (result.status !== 0) throw new Error(`${command} failed: ${result.stdout}\n${result.stderr}`);
  return result.stdout.trim();
};
run("git", ["clone", "--depth", "1", "--branch", "v24.1", "https://github.com/PlayPro/CoreProtect.git", "."]);
assert.equal(run("git", ["rev-parse", "HEAD"]), revision);
const maven = process.env.MAVEN_COMMAND || (process.platform === "win32" ? "mvn.cmd" : "mvn");
// cmd.exe is needed only to invoke the caller-selected Windows Maven launcher.
if (process.platform === "win32") run("cmd.exe", ["/d", "/s", "/c", `""${maven}" -q package -Dproject.branch=development"`]);
else run(maven, ["-q", "package", "-Dproject.branch=development"]);
const bytes = await readFile(join(source, "target", "CoreProtect-24.1.jar"));
const dir = resolve(destination);
await mkdir(dir, { recursive: true });
await copyFile(join(source, "target", "CoreProtect-24.1.jar"), join(dir, "CoreProtect.jar"));
const manifest = { version: "24.1", revision, branch: "development", source, sha256: createHash("sha256").update(bytes).digest("hex") };
await writeFile(join(dir, "coreprotect-fixture.json"), JSON.stringify(manifest, null, 2));
console.log(JSON.stringify(manifest));
