// Real-server integration scenario. Requires an explicitly supplied Paper-compatible
// server JAR; no fake Bukkit implementation or mocked plugin API is used.
import { spawn, spawnSync } from "node:child_process";
import { mkdtemp, mkdir, copyFile, writeFile, readdir, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "node:net";
import { randomUUID } from "node:crypto";
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
const bedrockPort = await freePort();
const origin = `http://127.0.0.1:${servicePort}`;
const admin = "live-scenario-service-token-32-characters";
const binary = join(dir, process.platform === "win32" ? "isles.exe" : "isles");
const build = spawnSync("go", ["build", "-o", binary, "./cmd/isles"], { cwd: root, env: { ...process.env, GOWORK: "off" }, stdio: "inherit" });
if (build.status !== 0) throw new Error("Build Isles before live scenario failed");
const service = spawn(binary, [], { env: { ...process.env, ISLES_LISTEN: `127.0.0.1:${servicePort}`, ISLES_DATA_DIR: join(dir, "data"), ISLES_ADMIN_TOKEN: admin, ISLES_SECRET_KEY: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" } });
let game, bot, bedrock, voiceClient;
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
async function connectPlayer(username, version = "1.21.1") {
  const client = mineflayer.createBot({ host: "127.0.0.1", port: gamePort, username, auth: "offline", version, hideErrors: true });
  client.on("error", (error) => console.log(`Client: ${error.message}`));
  try { await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("Game login timed out")), 30_000);
    const denied = (reason) => { clearTimeout(timer); reject(new Error(`Game login rejected: ${JSON.stringify(reason)}`)); };
    client.once("spawn", () => { clearTimeout(timer); resolve(); });
    client.once("kicked", denied);
    client.once("end", denied);
    client.once("error", (error) => denied(error.message));
  }); }
  catch (error) { client.end(); throw error; }
  return client;
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
  await writeFile(join(work, "server.properties"), `server-ip=127.0.0.1\nserver-port=${gamePort}\nonline-mode=false\nenforce-secure-profile=false\nspawn-protection=0\nview-distance=2\nsimulation-distance=2\ngenerate-structures=false\nspawn-monsters=false\nspawn-animals=false\nspawn-npcs=false\nmax-players=5\n`);
  await writeFile(join(work, "plugins", "MythicIsles", "config.yml"), `url: '${origin}'\nserver: live\ntoken: '${secret.token}'\nplaceholders:\n  server_name: '%server_name%'\n`);
  if (process.env.ISLES_TEST_DISABLE_SPARK === "1") {
    await mkdir(join(work, "config"), { recursive: true });
    await writeFile(join(work, "config", "paper-global.yml"), "_version: 29\nspark:\n  enabled: false\n");
  }
  if ((await readdir(join(work, "plugins"))).includes("Plan.jar")) {
    await mkdir(join(work, "plugins", "Plan"), { recursive: true });
    await writeFile(join(work, "plugins", "Plan", "config.yml"), `Webserver:\n  Internal_IP: 127.0.0.1\n  Port: ${await freePort()}\nData_gathering:\n  Geolocations: false\nPlugin:\n  Update_notifications:\n    Check_for_updates: false\n`);
  }
  if ((await readdir(join(work, "plugins"))).includes("voicechat.jar")) {
    await mkdir(join(work, "plugins", "voicechat"), { recursive: true });
    await writeFile(join(work, "plugins", "voicechat", "voicechat-server.properties"), `bind_address=127.0.0.1\nport=${await freePort()}\nkeep_alive=1000\n`);
  }
  if ((await readdir(join(work, "plugins"))).includes("Geyser-Spigot.jar")) {
    const floodgate = (await readdir(join(work, "plugins"))).includes("floodgate-spigot.jar");
    await mkdir(join(work, "plugins", "Geyser-Spigot"), { recursive: true });
    // Loopback-only unsigned protocol fixture. This is NOT a production config
    // or a claim that an Xbox account was authenticated.
    await writeFile(join(work, "plugins", "Geyser-Spigot", "config.yml"), `bedrock:\n  address: 127.0.0.1\n  port: ${bedrockPort}\nremote:\n  address: auto\n  auth-type: ${floodgate ? "floodgate" : "offline"}\nenable-proxy-connections: true\ncommand-suggestions: false\nmetrics:\n  enabled: false\nconfig-version: 4\n`);
  }
  game = spawn("java", ["-Xms256M", "-Xmx1536M", "-jar", "server.jar", "--nogui"], { cwd: work });
  capture(game, "paper");
  const heartbeat = await waitFor(async () => { if (game.exitCode !== null) throw new Error("Game server exited before heartbeat"); const result = await api("admin/servers/live/bridge"); return result.fresh && result; }, 240_000);
  assert(heartbeat.snapshot.platform);
  const pair = await api("players/website-player/pair", "POST", { server: "live" }, admin, "website-player");
  bot = await connectPlayer("IslesTester");
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
  let voicechat = "not-installed";
  if (currentProviders.voicechat) {
    const profile = async () => (await api("players/website-player/links", "GET", undefined, admin, "website-player"))[0];
    const noClient = await waitFor(async () => { const value = (await profile()).player?.voicechat; return value && !value.installed && !value.connected && value; });
    const { connectVoicechat } = await import("./voicechat-client.mjs");
    voiceClient = await connectVoicechat(bot);
    voiceClient.setDisabled(false);
    const connected = await waitFor(async () => { const value = (await profile()).player?.voicechat; return value?.installed && value.connected && !value.disabled && value; });
    voiceClient.setDisabled(true);
    const disabled = await waitFor(async () => { const value = (await profile()).player?.voicechat; return value?.connected && value.disabled && value; });
    voiceClient.setDisabled(false);
    await waitFor(async () => (await profile()).player?.voicechat?.disabled === false);
    voiceClient.close(); voiceClient = undefined;
    const disconnected = await waitFor(async () => { const value = (await profile()).player?.voicechat; return value?.installed && !value.connected && value; });
    bot.quit(); bot = undefined;
    await waitFor(async () => !(await profile()).player);
    await writeFile(join(dir, "voicechat-observations.json"), JSON.stringify({ noClient, connected, disabled, disconnected, offline: "player-observation-removed", scope: "protocol-only-loopback-no-microphone-or-audio-playback" }, null, 2));
    bot = await connectPlayer("IslesTester");
    await waitFor(async () => { const value = (await profile()).player?.voicechat; return value && !value.installed && !value.connected; });
    voicechat = "no-client-udp-auth-connect-disable-enable-timeout-offline-and-reconnect-observed";
  }
  let crossplay = "not-installed";
  if (currentProviders["Geyser-Spigot"]) {
    const versions = await waitFor(async () => (await api("admin/servers/live/bridge")).snapshot.crossplay);
    assert(versions.bedrockVersions.length > 0);
    const javaProfile = (await api("players/website-player/links", "GET", undefined, admin, "website-player"))[0];
    assert.equal(javaProfile.identity.platform, "java");
    assert.equal(javaProfile.player.connection, undefined, "Java client was classified by Floodgate prefix/UUID heuristics");
    const bedrockProtocol = (await import("bedrock-protocol")).default;
    bedrock = bedrockProtocol.createClient({ host: "127.0.0.1", port: bedrockPort, username: "BedrockTester", offline: true, version: "1.21.100", raknetBackend: "raknet-native", skipPing: true, viewDistance: 2, autoInitPlayer: false });
    bedrock.on("error", (error) => console.log(`Bedrock: ${error.message}`));
    const packetNames = [];
    bedrock.on("packet", (packet) => { if (packetNames.length < 1000) packetNames.push(packet.data.name); });
    await new Promise((resolve, reject) => { const timer = setTimeout(() => reject(new Error(`Bedrock start_game timed out: ${packetNames.join(",")}`)), 90_000); bedrock.once("start_game", () => { clearTimeout(timer); resolve(); }); bedrock.once("kick", (reason) => { clearTimeout(timer); reject(new Error(`Bedrock kicked: ${JSON.stringify(reason)}`)); }); });
    bedrock.write("set_local_player_as_initialized", { runtime_entity_id: bedrock.entityId });
    const pair = await api("players/bedrock-owner/pair", "POST", { server: "live" }, admin, "bedrock-owner");
    bedrock.write("command_request", { command: `/mythic link ${pair.code}`, origin: { type: "player", uuid: bedrock.profile.uuid, request_id: randomUUID(), player_entity_id: 0n }, internal: false, version: 66 });
    const linkedBedrock = await waitFor(async () => (await api("players/bedrock-owner/links", "GET", undefined, admin, "bedrock-owner"))[0]);
    assert.equal(linkedBedrock.identity.platform, "bedrock");
    const observed = await waitFor(async () => { const profile = (await api("players/bedrock-owner/links", "GET", undefined, admin, "bedrock-owner"))[0]; return profile?.player?.connection && profile; });
    assert.equal(observed.player.uuid, observed.identity.uuid);
    assert.equal(observed.player.connection.version, "1.21.100");
    assert.equal(observed.player.connection.provider, currentProviders.floodgate ? "floodgate" : "geyser");
    const whitelist = await api("admin/servers/live/tasks", "POST", { player: "bedrock-owner", kind: "whitelist", grant: true });
    await waitFor(async () => (await api("admin/servers/live/tasks")).some((task) => task.id === whitelist.id && task.status === "applied"));
    const whitelisted = JSON.parse(await readFile(join(work, "whitelist.json"), "utf8"));
    assert(whitelisted.some((entry) => entry.uuid === observed.identity.uuid), "Membership targeted a generated/prefixed UUID instead of Bukkit's actual UUID");
    await api("admin/servers/bedrock", "PUT", { name: "Bedrock endpoint", edition: "bedrock", address: `127.0.0.1:${bedrockPort}`, published: true, links: [] });
    assert.equal((await api("servers/bedrock/status")).reachable, true);
    await writeFile(join(dir, "crossplay-observations.json"), JSON.stringify({ versions, javaProfile, bedrock: observed, whitelist: "actual-bukkit-uuid", authentication: "unsigned-loopback-protocol-fixture-not-Xbox-authentication" }, null, 2));
    bedrock.disconnect(); bedrock = undefined;
    crossplay = "bedrock-udp-start-game-in-game-binding-version-and-bukkit-whitelist-observed";
  }
  let gc = "not-installed";
  if (currentProviders.spark) {
    await waitFor(async () => { const stats = (await api("admin/servers/live/bridge")).snapshot; return stats.tps > 0 && stats.mspt >= 0 && Number.isFinite(stats.cpu); });
    const before = await waitFor(async () => (await api("admin/servers/live/bridge")).snapshot.garbageCollectors);
    assert(Object.keys(before).length > 0);
    const forced = spawnSync("jcmd", [String(game.pid), "GC.run"], { encoding: "utf8", timeout: 30_000 });
    assert.equal(forced.status, 0, `JDK jcmd GC.run failed: ${forced.stderr ?? forced.error}`);
    const after = await waitFor(async () => {
      const value = (await api("admin/servers/live/bridge")).snapshot.garbageCollectors;
      return value && Object.entries(value).some(([name, stats]) => stats.collections > (before[name]?.collections ?? 0)) && value;
    });
    for (const [name, stats] of Object.entries(after)) {
      if (stats.collections !== undefined) assert(stats.collections >= 0);
      if (stats.timeMs !== undefined) assert(stats.timeMs >= (before[name]?.timeMs ?? 0));
    }
    assert.equal(JSON.stringify(await api("servers")).includes("garbageCollectors"), false);
    assert.equal(JSON.stringify(await api("players/website-player/links", "GET", undefined, admin, "website-player")).includes("garbageCollectors"), false);
    await writeFile(join(dir, "gc-observations.json"), JSON.stringify({ before, after, provider: "spark bundled with Purpur 1.21.1-2329", scope: "provider-normal-operation-baseline-not-jvm-lifetime-or-rolling-interval", trigger: "JDK jcmd GC.run" }, null, 2));
    gc = "provider-counters-increased-after-jcmd-gc-run";
  } else if (process.env.ISLES_TEST_DISABLE_SPARK === "1") {
    assert.equal((await api("admin/servers/live/bridge")).snapshot.garbageCollectors, undefined);
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
  const restartGame = async () => {
    bot?.quit(); bot = undefined;
    game.stdin.write("stop\n");
    await waitFor(() => game.exitCode !== null, 60_000);
    const restartedAt = Date.now();
    game = spawn("java", ["-Xms256M", "-Xmx1536M", "-jar", "server.jar", "--nogui"], { cwd: work });
    capture(game, "paper-restarted");
    await waitFor(async () => Date.parse((await api("admin/servers/live/bridge")).snapshot.receivedAt) > restartedAt, 240_000);
  };
  let legacyProtocols = "not-installed";
  if (currentProviders.ViaBackwards) {
    const allowed = (await api("admin/servers/live/bridge")).snapshot.protocolVersions;
    const clients = [];
    for (const [username, version, owner] of [
      ["LegacyTwelve", "1.12.2", "legacy-twelve-owner"],
      ...(currentProviders.ViaRewind ? [["LegacyEight", "1.8.8", "legacy-eight-owner"]] : []),
    ]) {
      const client = await connectPlayer(username, version);
      try {
        const code = await api(`players/${owner}/pair`, "POST", { server: "live" }, admin, owner);
        client.chat(`/mythic link ${code.code}`);
        const profile = await waitFor(async () => {
          const value = (await api(`players/${owner}/links`, "GET", undefined, admin, owner))[0];
          return value?.player?.clientVersion && value;
        });
        assert.equal(profile.identity.platform, "java");
        assert.equal(profile.identity.uuid.replaceAll("-", ""), client._client.uuid.replaceAll("-", ""));
        assert(allowed.includes(profile.player.clientVersion), "Connected protocol missing from provider support list");
        assert(profile.player.clientVersion.startsWith(version === "1.8.8" ? "1.8" : "1.12"));
        clients.push({ requestedVersion: version, identity: profile.identity, player: profile.player });
      } finally { client.quit(); }
    }
    let blocked;
    if (currentProviders.ViaRewind) {
      const configPath = join(work, "plugins", "ViaVersion", "config.yml");
      const config = await readFile(configPath, "utf8");
      assert(/^block-protocols: \[\]/m.test(config));
      await writeFile(configPath, config.replace(/^block-protocols: \[\]/m, "block-protocols: [47]").replace(/^block-disconnect-msg:.*$/m, "block-disconnect-msg: Isles blocked protocol fixture"));
      await restartGame();
      blocked = await waitFor(async () => {
        const versions = (await api("admin/servers/live/bridge")).snapshot.protocolVersions;
        return versions && !versions.some((version) => version.startsWith("1.8")) && versions;
      });
      assert(blocked.some((version) => version.startsWith("1.12")));
      await assert.rejects(connectPlayer("BlockedEight", "1.8.8"), /Isles blocked protocol fixture/);
      bot = await connectPlayer("IslesTester");
    }
    await writeFile(join(dir, "legacy-protocol-observations.json"), JSON.stringify({ allowed, clients, ...(blocked ? { blocked, blockedProtocol: 47, rejectedClient: "1.8.8" } : {}), providers: Object.fromEntries(Object.entries(currentProviders).filter(([name]) => ["ViaVersion", "ViaBackwards", "ViaRewind"].includes(name))), scope: "offline-loopback-client-spawn-binding-and-provider-version-not-all-gameplay-or-proxy-auth" }, null, 2));
    legacyProtocols = currentProviders.ViaRewind ? "1.12.2-and-1.8.8-bound-protocol47-blocked-and-rejected" : "1.12.2-spawn-binding-and-client-version-observed";
  } else if (currentProviders.ViaVersion) {
    const allowed = (await api("admin/servers/live/bridge")).snapshot.protocolVersions;
    assert(!allowed.some((version) => version.startsWith("1.12")), "ViaVersion alone advertised a backwards conversion");
    await assert.rejects(connectPlayer("UnsupportedOld", "1.12.2"), /Game login rejected:/);
    legacyProtocols = "viaversion-only-does-not-advertise-or-accept-1.12.2";
  }
  let towny = "not-installed";
  if (currentProviders.Towny) {
    const membership = () => api("players/website-player/links", "GET", undefined, admin, "website-player");
    const townless = await waitFor(async () => (await membership())[0]?.towny);
    assert.equal(townless.town, undefined);
    game.stdin.write("op IslesTester\n");
    await pause(1000);
    bot.chat("/town new IslesHarbor");
    const mayor = await waitFor(async () => { const value = (await membership())[0]?.towny; return value?.town?.name === "IslesHarbor" && value.mayor && value; });
    bot.chat("/nation new IslesUnion");
    const king = await waitFor(async () => { const value = (await membership())[0]?.towny; return value?.town?.nation?.name === "IslesUnion" && value.king && value; });
    bot.chat("/town toggle open on");
    await waitFor(async () => (await membership())[0]?.towny?.town?.open);
    bot.chat("/town toggle public off");
    await waitFor(async () => (await membership())[0]?.towny?.town?.public === false);
    const guest = await connectPlayer("TownyGuest");
    await pause(1000);
    game.stdin.write("ta town IslesHarbor add TownyGuest\n");
    await waitFor(async () => (await membership())[0]?.towny?.town?.residents === 2);
    game.stdin.write("ta set mayor IslesHarbor TownyGuest\n");
    await waitFor(async () => { const value = (await membership())[0]?.towny; return value?.town && !value.mayor && !value.king; });
    game.stdin.write("ta town IslesHarbor rank add IslesTester assistant\n");
    const resident = await waitFor(async () => { const value = (await membership())[0]?.towny; return value?.townRanks?.includes("assistant") && value; });
    const aggregate = (await api("admin/servers/live/bridge")).snapshot.towny;
    assert.equal(aggregate.townCount, 1);
    assert.equal(aggregate.nationCount, 1);
    assert.equal(aggregate.residents.length, 1, "Unlinked resident was exported");
    assert.equal(resident.town.claimedTownBlocks >= 1, true);
    guest.quit();
    game.stdin.write("deop IslesTester\n");
    await restartGame();
    const afterRestart = await waitFor(async () => { const value = (await membership())[0]; return value?.fresh && !value.player && value.towny?.townRanks?.includes("assistant") && value; });
    assert.equal(afterRestart.towny.town.id, resident.town.id);
    game.stdin.write("ta town IslesHarbor kick IslesTester\n");
    const removed = await waitFor(async () => { const value = (await membership())[0]?.towny; return value && !value.town && value; });
    assert.equal(removed.mayor, false);
    await writeFile(join(dir, "towny-observations.json"), JSON.stringify({ townless, mayor, king, resident, aggregate, afterRestart, removed }, null, 2));
    towny = "town-nation-mayor-king-rank-offline-restart-and-removal-observed";
  }
  let plan = "not-installed";
  if (currentProviders.Plan) {
    await waitFor(async () => (await api("admin/servers/live/bridge")).snapshot.plan);
    bot?.quit(); bot = undefined;
    const activity = await waitFor(async () => (await api("players/website-player/links", "GET", undefined, admin, "website-player"))[0]?.activity);
    assert(activity.sessions >= 1 && activity.playtimeMs > 0);
    const peer = await connectPlayer("UnlinkedGuest");
    await pause(3000);
    peer.quit();
    const aggregate = await waitFor(async () => {
      const snapshot = (await api("admin/servers/live/bridge")).snapshot.plan;
      return snapshot?.recordedPlayers >= 2 && snapshot;
    });
    assert.equal(aggregate.players.length, 1, "Unlinked player activity was exported");
    assert.equal(aggregate.players[0].uuid, linked[0].identity.uuid);
    await restartGame();
    const afterRestart = await waitFor(async () => {
      const result = (await api("players/website-player/links", "GET", undefined, admin, "website-player"))[0];
      return result?.fresh && result.activity && !result.player && result;
    });
    assert.equal(afterRestart.activity.playtimeMs, activity.playtimeMs);
    assert.equal(afterRestart.activity.sessions, activity.sessions);
    await writeFile(join(dir, "plan-observations.json"), JSON.stringify({ activity, aggregate, afterRestart }, null, 2));
    plan = "saved-sessions-offline-owner-only-aggregate-and-restart-observed";
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
  const manifest = { server: heartbeat.snapshot.platform, minecraft: heartbeat.snapshot.minecraft, heartbeat: true, inGameLink: true, gameName: linked[0].identity.name, whitelist: "applied", member, placeholder, protocols, regions, economy, chunky, plan, towny, crossplay, voicechat, gc, legacyProtocols, providers: Object.keys((await api("admin/servers/live/bridge")).snapshot.plugins).sort() };
  await writeFile(join(dir, "scenario-result.json"), JSON.stringify(manifest, null, 2));
  console.log(JSON.stringify(manifest));
} finally {
  voiceClient?.close();
  bedrock?.disconnect();
  bot?.quit();
  if (game && game.exitCode === null) {
    game.stdin.write("stop\n");
    await Promise.race([new Promise((r) => game.once("exit", r)), pause(15_000)]);
    if (game.exitCode === null) game.kill();
  }
  service.kill();
  await writeFile(join(dir, "process.log"), outputs.join(""));
}
