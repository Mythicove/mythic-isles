// Live Modrinth acceptance through the production Isles binary. Supply an
// existing local published artifact and its expected Modrinth version ID.
import { spawn, spawnSync } from "node:child_process";
import { readFile, mkdtemp, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const [file, expectedVersion] = process.argv.slice(2);
if (!file || !expectedVersion) throw new Error("Usage: node scripts/catalog-live.mjs /path/to/published.jar modrinth-version-id");
const root = fileURLToPath(new URL("../", import.meta.url));
const dir = await mkdtemp(join(tmpdir(), "isles-catalog-live-"));
console.log(`Scenario directory: ${dir}`);
const bytes = await readFile(file);
const hash = createHash("sha512").update(bytes).digest("hex");
const listener = createServer();
await new Promise((resolve, reject) => { listener.on("error", reject); listener.listen(0, "127.0.0.1", resolve); });
const port = listener.address().port;
await new Promise((resolve) => listener.close(resolve));
const executable = join(dir, process.platform === "win32" ? "isles.exe" : "isles");
const built = spawnSync("go", ["build", "-o", executable, "./cmd/isles"], { cwd: root, env: { ...process.env, GOWORK: "off" }, stdio: "inherit" });
assert.equal(built.status, 0);
const service = spawn(executable, [], { env: { ...process.env, ISLES_MODRINTH_URL: "https://api.modrinth.com/v2", ISLES_HANGAR_URL: "https://hangar.papermc.io/api/v1", ISLES_LISTEN: `127.0.0.1:${port}`, ISLES_DATA_DIR: join(dir, "data"), ISLES_ADMIN_TOKEN: "live-catalog-scenario-token-32-characters", ISLES_SECRET_KEY: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" } });
let logs = "";
for (const stream of [service.stdout, service.stderr]) stream.on("data", (chunk) => { logs += chunk; });
const origin = `http://127.0.0.1:${port}`;
try {
  const deadline = Date.now() + 30_000;
  for (;;) {
    try { if ((await fetch(`${origin}/healthz`)).ok) break; } catch { /* wait for process startup */ }
    if (Date.now() > deadline || service.exitCode !== null) throw new Error("Isles startup failed");
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const response = await fetch(`${origin}/api/isles/catalog/modrinth/hash/${hash}`, { signal: AbortSignal.timeout(60_000) });
  assert.equal(response.status, 200, await response.clone().text());
  const match = await response.json();
  assert.equal(match.version.id, expectedVersion);
  assert(match.projectId);
  assert(match.version.files.length > 0);
  assert(match.version.files.every((file) => file.hashes.sha512 === hash));
  assert(match.version.files.some((file) => file.size === bytes.length));
  const cached = await fetch(`${origin}/api/isles/catalog/modrinth/hash/${hash.toUpperCase()}`);
  assert.deepEqual(await cached.json(), match);
  const modifiedHash = createHash("sha512").update(bytes).update("modified-fixture").digest("hex");
  const missing = await fetch(`${origin}/api/isles/catalog/modrinth/hash/${modifiedHash}`, { signal: AbortSignal.timeout(60_000) });
  assert.equal(missing.status, 404);
  const manifest = { source: "https://api.modrinth.com/v2", algorithm: "sha512", hash, bytes: bytes.length, projectId: match.projectId, versionId: match.version.id, publisherFiles: match.version.files.map((file) => file.name).sort(), normalizedRepeat: "same-result", modifiedBytes: "not-indexed", scope: "file-identity-not-installation-or-execution" };
  await writeFile(join(dir, "scenario-result.json"), JSON.stringify(manifest, null, 2));
  await writeFile(join(dir, "modrinth-match.json"), JSON.stringify(match, null, 2));
  console.log(JSON.stringify(manifest));
} finally {
  service.kill();
  await writeFile(join(dir, "process.log"), logs);
}
