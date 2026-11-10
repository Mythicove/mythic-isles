// Real-server integration scenario. Requires an explicitly supplied Paper-compatible
// server JAR; no fake Bukkit implementation or mocked plugin API is used.
import { spawn, spawnSync } from "node:child_process";
import { mkdtemp, mkdir, copyFile, writeFile, readdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "node:net";
import assert from "node:assert/strict";
import mineflayer from "mineflayer";

const root = fileURLToPath(new URL("../../../", import.meta.url));
const paper = process.env.PAPER_SERVER_JAR;
if (!paper) throw new Error("Set PAPER_SERVER_JAR to an installed Paper-compatible server JAR.");
const dir = await mkdtemp(join(tmpdir(), "isles-paper-live-"));
console.log(`Scenario directory: ${dir}`);
const freePort = () => new Promise((resolve, reject) => { const s = createServer(); s.on("error", reject); s.listen(0, "127.0.0.1", () => { const port = s.address().port; s.close(() => resolve(port)); }); });
const servicePort = await freePort();
const gamePort = await freePort();
const origin = `http://127.0.0.1:${servicePort}`;
const admin = "live-scenario-service-token-32-characters";
const binary = join(dir, process.platform === "win32" ? "isles.exe" : "isles");
const build = spawnSync("go", ["build", "-o", binary, "./cmd/isles"], { cwd: root, env: { ...process.env, GOWORK: "off" }, stdio: "inherit" });
if (build.status !== 0) throw new Error("Build Isles before live scenario failed");
const service = spawn(binary, [], { env: { ...process.env, ISLES_LISTEN: `127.0.0.1:${servicePort}`, ISLES_DATA_DIR: join(dir, "data"), ISLES_ADMIN_TOKEN: admin, ISLES_SECRET_KEY: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" } });
let game, bot;
const outputs = [];
const capture = (p, name) => { for (const stream of [p.stdout, p.stderr]) stream?.on("data", (data) => { const text = data.toString(); outputs.push(`[${name}] ${text}`); if (/ERROR|Exception|Done \(/.test(text)) console.log(text); }); };
capture(service, "isles");
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
async function api(path, method = "GET", body, credential = admin, player) {
  const headers = { Authorization: `Bearer ${credential}`, "Content-Type": "application/json" };
  if (player) headers["X-Isles-Player"] = player;
  const res = await fetch(`${origin}/api/isles/${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(15_000) });
  if (!res.ok) throw new Error(`HTTP ${res.status} on ${path}: ${await res.text()}`);
  if (res.status === 204 || res.status === 202) return;
  return res.json();
}
async function waitFor(check, ms = 120_000) {
  const end = Date.now() + ms;
  let error;
  while (Date.now() < end) { try { const value = await check(); if (value) return value; } catch (e) { error = e; } await pause(1000); }
  throw error ?? new Error("Scenario condition timed out");
}
try {
  await waitFor(async () => (await fetch(`${origin}/healthz`)).ok, 30_000);
  await api("admin/servers/live", "PUT", { name: "Live test", edition: "java", address: `127.0.0.1:${gamePort}`, published: true, links: [] });
  const secret = await api("admin/servers/live/bridge-token", "POST");
  const work = join(dir, "server");
  await mkdir(join(work, "plugins", "MythicIsles"), { recursive: true });
  await copyFile(resolve(paper), join(work, "server.jar"));
  await copyFile(join(root, "bridge", "paper", "target", "isles-paper-bridge-0.1.0.jar"), join(work, "plugins", "MythicIsles.jar"));
  if (process.env.ISLES_TEST_PLUGIN_DIR) {
    const selected = process.env.ISLES_TEST_PLUGIN_FILTER?.split(",");
    for (const name of await readdir(process.env.ISLES_TEST_PLUGIN_DIR)) if (name.endsWith(".jar") && (!selected || selected.includes(name))) await copyFile(join(process.env.ISLES_TEST_PLUGIN_DIR, name), join(work, "plugins", name));
  }
  await writeFile(join(work, "eula.txt"), "eula=true\n");
  await writeFile(join(work, "server.properties"), `server-ip=127.0.0.1\nserver-port=${gamePort}\nonline-mode=false\nenforce-secure-profile=false\nspawn-protection=0\nview-distance=2\nsimulation-distance=2\ngenerate-structures=false\nmax-players=5\n`);
  await writeFile(join(work, "plugins", "MythicIsles", "config.yml"), `url: '${origin}'\nserver: live\ntoken: '${secret.token}'\nplaceholders:\n  server_name: '%server_name%'\n`);
  game = spawn("java", ["-Xms256M", "-Xmx1536M", "-jar", "server.jar", "--nogui"], { cwd: work });
  capture(game, "paper");
  const heartbeat = await waitFor(async () => { if (game.exitCode !== null) throw new Error("Game server exited before heartbeat"); const result = await api("admin/servers/live/bridge"); return result.fresh && result; }, 240_000);
  assert(heartbeat.snapshot.platform);
  const pair = await api("players/website-player/pair", "POST", { server: "live" }, admin, "website-player");
  bot = mineflayer.createBot({ host: "127.0.0.1", port: gamePort, username: "IslesTester", auth: "offline", version: "1.21.1", hideErrors: true });
  bot.on("error", (error) => console.log(`Client: ${error.message}`));
  await new Promise((resolve, reject) => { const timer = setTimeout(() => reject(new Error("Game login timed out")), 30_000); bot.once("spawn", () => { clearTimeout(timer); resolve(); }); bot.once("kicked", (reason) => { clearTimeout(timer); reject(new Error(`Kicked: ${JSON.stringify(reason)}`)); }); });
  bot.chat(`/mythic link ${pair.code}`);
  const linked = await waitFor(async () => { const items = await api("players/website-player/links", "GET", undefined, admin, "website-player"); return items.length && items; });
  assert.equal(linked[0].identity.name, "IslesTester");
  const task = await api("admin/servers/live/tasks", "POST", { player: "website-player", kind: "whitelist", grant: true });
  await waitFor(async () => { const tasks = await api("admin/servers/live/tasks"); const t = tasks.find((t) => t.id === task.id); if (t?.status === "failed") throw new Error(t.error); return t?.status === "applied"; });
  await waitFor(async () => { const items = await api("players/website-player/links", "GET", undefined, admin, "website-player"); return items[0]?.fresh && items[0]?.player; });
  let member = "not-installed";
  if (heartbeat.snapshot.plugins.LuckPerms) {
    game.stdin.write("lp creategroup member\n");
    await waitFor(async () => (await api("admin/servers/live/bridge")).snapshot.groups.includes("member"));
    const granted = await api("admin/servers/live/tasks", "POST", { player: "website-player", kind: "member", group: "member", grant: true });
    await waitFor(async () => (await api("admin/servers/live/tasks")).some((t) => t.id === granted.id && t.status === "applied"));
    await waitFor(async () => (await api("players/website-player/links", "GET", undefined, admin, "website-player"))[0]?.player?.groups?.includes("member"));
    const revoked = await api("admin/servers/live/tasks", "POST", { player: "website-player", kind: "member", group: "member", grant: false });
    await waitFor(async () => (await api("admin/servers/live/tasks")).some((t) => t.id === revoked.id && t.status === "applied"));
    await waitFor(async () => { const current = (await api("players/website-player/links", "GET", undefined, admin, "website-player"))[0]?.player; return current && !current.groups.includes("member"); });
    member = "granted-and-revoked";
  }
  const currentProviders = (await api("admin/servers/live/bridge")).snapshot.plugins;
  if (currentProviders.spark) {
    await waitFor(async () => { const stats = (await api("admin/servers/live/bridge")).snapshot; return stats.tps > 0 && stats.mspt >= 0 && Number.isFinite(stats.cpu); });
  }
  let placeholder = "not-installed";
  if (heartbeat.snapshot.plugins.PlaceholderAPI) {
    game.stdin.write("papi ecloud download Server\n");
    await pause(5000);
    game.stdin.write("papi reload\n");
    await waitFor(async () => (await api("players/website-player/links", "GET", undefined, admin, "website-player"))[0]?.player?.values?.server_name);
    placeholder = "resolved";
  }
  let protocols = "not-installed";
  if (currentProviders.ViaVersion) {
    await waitFor(async () => (await api("admin/servers/live/bridge")).snapshot.protocolVersions?.some((v) => v.includes("1.21")));
    await waitFor(async () => (await api("players/website-player/links", "GET", undefined, admin, "website-player"))[0]?.player?.clientVersion?.includes("1.21"));
    protocols = "server-and-player-observed";
  }
  let regions = "not-installed";
  if (currentProviders.WorldGuard) {
    game.stdin.write(`rg define -w world -g isles_test\n`);
    await pause(1000);
    game.stdin.write(`rg addmember -w world isles_test ${linked[0].identity.uuid}\n`);
    // A global/template region has no physical location and must not appear.
    await pause(1000);
    game.stdin.write(`op IslesTester\n`);
    await pause(1000);
    bot.chat("//pos1 0,60,0");
    await pause(1000);
    bot.chat("//pos2 10,80,10");
    await pause(1000);
    bot.chat("/rg define isles_plot");
    await pause(1000);
    game.stdin.write(`rg addmember -w world isles_plot ${linked[0].identity.uuid}\n`);
    await waitFor(async () => { const profile = (await api("players/website-player/links", "GET", undefined, admin, "website-player"))[0]?.player; return profile?.regions?.some((r) => r.id === "isles_plot" && r.world === "world" && r.centerX === 5 && r.role === "member") && !profile.regions.some((r) => r.id === "isles_test"); });
    game.stdin.write("deop IslesTester\n");
    regions = "membership-and-location-observed";
  }
  let economy = "not-installed";
  if (currentProviders.Vault) {
    game.stdin.write("eco set IslesTester 123.45\n");
    let observation;
    try { await waitFor(async () => { observation = (await api("players/website-player/links", "GET", undefined, admin, "website-player"))[0]; const value=observation?.player?.economy; return value && Math.abs(value.balance - 123.45) < 0.000001 && typeof value.formatted === "string" && value.formatted.length > 0 && value.provider; }); }
    finally { await writeFile(join(dir,"economy-observation.json"),JSON.stringify(observation ?? null,null,2)); }
    economy = "balance-and-publisher-format-observed";
  }
  let chunky = "not-installed";
  if (currentProviders.Chunky) {
    const operation = async (action, extra = {}) => {
      const task = await api("admin/servers/live/chunky", "POST", { world: "world", action, ...extra });
      await waitFor(async () => {
        const result = (await api("admin/servers/live/tasks")).find((t) => t.id === task.id);
        if (result?.status === "failed") throw new Error(`Chunky ${action}: ${result.error}`);
        return result?.status === "applied";
      });
    };
    const worldState = (state) => waitFor(async () => {
      const world = (await api("admin/servers/live/bridge")).snapshot.chunky?.find((w) => w.world === "world");
      return world?.state === state && world;
    });
    const restartGame = async () => {
      bot?.quit(); bot = undefined;
      game.stdin.write("stop\n");
      await waitFor(() => game.exitCode !== null, 60_000);
      const restartedAt = Date.now();
      game = spawn("java", ["-Xms256M", "-Xmx1536M", "-jar", "server.jar", "--nogui"], { cwd: work });
      capture(game, "paper-restarted");
      await waitFor(async () => Date.parse((await api("admin/servers/live/bridge")).snapshot.receivedAt) > restartedAt, 240_000);
    };
    await worldState("idle");
    await operation("start", { shape: "square", centerX: 10000, centerZ: 10000, radius: 1024 });
    const running = await worldState("running");
    assert(running.progress.total > 0);
    await operation("pause");
    const paused = await worldState("paused");
    assert(paused.progress.chunks > 0, "No real chunks were generated");
    // A game-server restart must reconstruct paused state from Chunky's own
    // task store, not from an in-memory Bridge event history.
    await restartGame();
    await worldState("paused");
    await operation("continue");
    const resumed = await worldState("running");
    await operation("cancel");
    const cancelled = await worldState("cancelled");
    await operation("start", { shape: "circle", centerX: 20000, centerZ: 20000, radius: 1024 });
    await worldState("running");
    await operation("pause");
    await worldState("paused");
    await operation("cancel");
    await worldState("cancelled");
    await operation("start", { shape: "square", centerX: 0, centerZ: 0, radius: 32 });
    const completed = await worldState("completed");
    assert.equal(completed.progress.percent, 100);
    assert(completed.progress.chunks >= completed.progress.total);
    assert.equal(completed.progress.etaSeconds, undefined, "Elapsed completion time was mislabeled as ETA");
    await restartGame();
    const terminalAfterRestart = (await api("admin/servers/live/bridge")).snapshot.chunky.find((w) => w.world === "world");
    assert(["stopped", "completed"].includes(terminalAfterRestart.state), "Saved cancellation flag was misrepresented as known cancellation");
    await writeFile(join(dir, "chunky-observations.json"), JSON.stringify({ running, paused, resumed, cancelled, completed, terminalAfterRestart }, null, 2));
    chunky = "generated-paused-restarted-resumed-cancelled-running-and-paused-completed";
  }
  const manifest = { server: heartbeat.snapshot.platform, minecraft: heartbeat.snapshot.minecraft, heartbeat: true, inGameLink: true, gameName: linked[0].identity.name, whitelist: "applied", member, placeholder, protocols, regions, economy, chunky, providers: Object.keys((await api("admin/servers/live/bridge")).snapshot.plugins).sort() };
  await writeFile(join(dir, "scenario-result.json"), JSON.stringify(manifest, null, 2));
  console.log(JSON.stringify(manifest));
} finally {
  bot?.quit();
  if (game && game.exitCode === null) {
    game.stdin.write("stop\n");
    await Promise.race([new Promise((r) => game.once("exit", r)), pause(15_000)]);
    if (game.exitCode === null) game.kill();
  }
  service.kill();
  await writeFile(join(dir, "process.log"), outputs.join(""));
}
