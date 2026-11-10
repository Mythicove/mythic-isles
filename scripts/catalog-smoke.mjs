// Opt-in live upstream check: starts the production service and reads metadata
// from the official catalogs. It does not download or execute project files.
import { spawn, spawnSync } from "node:child_process";
import { mkdtemp, writeFile } from "node:fs/promises";
import { randomBytes } from "node:crypto";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const root = fileURLToPath(new URL("../", import.meta.url));
const dir = await mkdtemp(join(tmpdir(), "isles-catalog-"));
const binary = join(dir, process.platform === "win32" ? "isles.exe" : "isles");
const built = spawnSync("go", ["build", "-o", binary, "./cmd/isles"], { cwd: root, env: { ...process.env, GOWORK: "off" }, stdio: "inherit" });
if (built.status !== 0) throw new Error("Isles build failed");
const port = await new Promise((resolve, reject) => { const s = createServer(); s.once("error", reject); s.listen(0, "127.0.0.1", () => { const port = s.address().port; s.close(() => resolve(port)); }); });
const origin = `http://127.0.0.1:${port}`;
const service = spawn(binary, [], { env: { ...process.env, ISLES_LISTEN: `127.0.0.1:${port}`, ISLES_DATA_DIR: join(dir, "data"), ISLES_ADMIN_TOKEN: randomBytes(32).toString("hex"), ISLES_SECRET_KEY: randomBytes(32).toString("base64"), ISLES_MODRINTH_URL: "https://api.modrinth.com/v2", ISLES_HANGAR_URL: "https://hangar.papermc.io/api/v1" }, stdio: ["ignore", "ignore", "inherit"] });
async function get(path) {
  const res = await fetch(`${origin}/api/isles/catalog/${path}`, { signal: AbortSignal.timeout(25_000) });
  if (!res.ok) throw new Error(`${path}: HTTP ${res.status}: ${await res.text()}`);
  return res.json();
}
try {
  let ready = false;
  for (let i = 0; i < 50; i++) { try { if ((await fetch(`${origin}/healthz`)).ok) { ready = true; break; } } catch {} await new Promise((r) => setTimeout(r, 100)); }
  assert(ready, "service did not start");
  const modrinth = await get("modrinth/search?q=Fabric%20API&loader=fabric&game_version=1.21.1&limit=3");
  assert(modrinth.length > 0 && modrinth[0].source === "modrinth");
  const mods = await get(`modrinth/versions/${encodeURIComponent(modrinth[0].id)}?loader=fabric&game_version=1.21.1&limit=2`);
  assert(mods.length > 0 && mods[0].files.some((f) => f.hashes.sha512));
  const hangar = await get("hangar/search?q=ViaVersion&platform=PAPER&limit=3");
  const project = hangar.find((p) => p.slug === "ViaVersion");
  assert(project, "ViaVersion absent from Hangar results");
  const plugins = await get(`hangar/versions/${project.id.split("/").map(encodeURIComponent).join("/")}?platform=PAPER&limit=2`);
  assert(plugins.length > 0 && plugins[0].files.some((f) => f.platform === "PAPER" && f.hashes.sha256));
  const manifest = { modrinth: { project: modrinth[0].id, version: mods[0].id, files: mods[0].files.length }, hangar: { project: project.id, version: plugins[0].id, files: plugins[0].files.length }, filesDownloaded: false };
  await writeFile(join(dir, "scenario-result.json"), JSON.stringify(manifest, null, 2));
  console.log(JSON.stringify({ directory: dir, ...manifest }));
} finally { service.kill(); }
