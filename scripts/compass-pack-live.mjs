// Cross-product acceptance: real Isles publication -> untouched .mrpack download
// -> Compass production import/launch core. Requires Java 21, Rust, Go, network
// and a desktop/OpenGL session. External pack source uses only loopback fixtures.
import { spawn, spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { mkdtemp, writeFile, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash, generateKeyPairSync } from "node:crypto";
import assert from "node:assert/strict";

const compass = process.argv[2];
if (!compass) throw new Error("Usage: node scripts/compass-pack-live.mjs /absolute/compass-root");
const root = fileURLToPath(new URL("../", import.meta.url));
const run = await mkdtemp(join(tmpdir(), "isles-compass-pack-"));
console.log(`Scenario directory: ${run}`);
const hash = (data) => createHash("sha256").update(data).digest("hex");
const sources = new Map();
const reads = [];
const { publicKey } = generateKeyPairSync("rsa", { modulusLength: 2048, publicKeyEncoding: { type: "spki", format: "pem" }, privateKeyEncoding: { type: "pkcs8", format: "pem" } });
const fixture = createServer((req, res) => {
  reads.push(req.url);
  if (req.url === "/fixture/corrupt-optional" && req.method === "POST") {
    sources.set("/optional.txt", "optional client fail\n");
    return res.end("{}");
  }
  if (req.url === "/") {
    res.setHeader("Content-Type", "application/json");
    return res.end(JSON.stringify({ meta: { serverName: "Isles acceptance", implementationName: "protocol-fixture", implementationVersion: "1" }, skinDomains: [], signaturePublickey: publicKey }));
  }
  const file = sources.get(req.url);
  if (file === undefined) { res.statusCode = 404; return res.end(); }
  res.end(file);
});
await new Promise((done) => fixture.listen(0, "127.0.0.1", done));
const fixtureOrigin = `http://127.0.0.1:${fixture.address().port}`;
const address = createServer();
await new Promise((done) => address.listen(0, "127.0.0.1", done));
const servicePort = address.address().port;
await new Promise((done) => address.close(done));
const origin = `http://127.0.0.1:${servicePort}`;
const token = "isles-compass-live-service-token-32-characters";
const binary = join(run, process.platform === "win32" ? "isles.exe" : "isles");
const built = spawnSync("go", ["build", "-o", binary, "./cmd/isles"], { cwd: root, env: { ...process.env, GOWORK: "off" }, stdio: "inherit" });
assert.equal(built.status, 0);
const service = spawn(binary, [], { env: { ...process.env, ISLES_LISTEN: `127.0.0.1:${servicePort}`, ISLES_DATA_DIR: join(run, "isles-data"), ISLES_ADMIN_TOKEN: token, ISLES_SECRET_KEY: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" } });
let logs = "";
for (const stream of [service.stdout, service.stderr]) stream.on("data", (chunk) => { logs += chunk; });
async function api(path, method = "GET", data) {
  const res = await fetch(origin + "/api/isles/" + path, { method, headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, body: data === undefined ? undefined : JSON.stringify(data), signal: AbortSignal.timeout(120_000) });
  assert(res.ok, `${path}: ${res.status} ${await res.clone().text()}`);
  return res.json();
}
try {
  // Fixed publisher version; pin both identity and digest independently of the
  // loopback packwiz source. The real installer downloads this official URL.
  const response = await fetch("https://api.modrinth.com/v2/version/5SwzqWUu", { signal: AbortSignal.timeout(30_000) });
  assert(response.ok);
  const version = await response.json();
  const mod = version.files.find((file) => file.primary);
  assert.equal(mod.hashes.sha512, "9f721c253ae2e9823cb548e8ec132c9928f80a65184e9b8dcfc489a8d632df20cbfea1e65ff8a4333bdb295c41fbe2228b43e4d41c7355d42ed4637a785840a1");
  sources.set("/config/mythic-isles.properties", "community=Isles acceptance\n");
  sources.set("/optional.txt", "optional client file\n");
  sources.set("/server.txt", "server-only file\n");
  sources.set("/mods/fabric-api.pw.toml", `name = "Fabric API"\nfilename = "fabric-api.jar"\nside = "both"\n[download]\nurl = ${JSON.stringify(mod.url)}\nhash-format = "sha512"\nhash = "${mod.hashes.sha512}"\n`);
  for (const [name, side, optional] of [["optional", "client", true], ["server", "server", false]]) {
    sources.set(`/config/${name}.pw.toml`, `name = "${name}"\nfilename = "${name}.txt"\nside = "${side}"\n[download]\nurl = "${fixtureOrigin}/${name}.txt"\nhash-format = "sha256"\nhash = "${hash(sources.get(`/${name}.txt`))}"\n[option]\noptional = ${optional}\ndefault = false\n`);
  }
  const entries = ["config/mythic-isles.properties", "mods/fabric-api.pw.toml", "config/optional.pw.toml", "config/server.pw.toml"];
  const index = "hash-format = \"sha256\"\n" + entries.map((name) => `\n[[files]]\nfile = "${name}"\nhash = "${hash(sources.get("/" + name))}"\nmetafile = ${name.endsWith(".pw.toml")}\n`).join("");
  sources.set("/index.toml", index);
  sources.set("/pack.toml", `name = "Isles Compass acceptance"\nversion = "1.0.0"\npack-format = "packwiz:1.1.0"\n[index]\nfile = "index.toml"\nhash-format = "sha256"\nhash = "${hash(index)}"\n[versions]\nminecraft = "1.21.1"\nfabric = "0.16.14"\n`);
  const deadline = Date.now() + 30_000;
  for (;;) {
    try { if ((await fetch(origin + "/healthz")).ok) break; } catch { /* wait for startup */ }
    assert(Date.now() < deadline && service.exitCode === null, "Isles startup failed");
    await new Promise((done) => setTimeout(done, 100));
  }
  await api("admin/servers/community", "PUT", { name: "Isles Compass acceptance", edition: "java", address: "127.0.0.1:25565", published: true, packwizUrl: fixtureOrigin + "/pack.toml", links: [] });
  const published = await api("admin/servers/community/publish-pack", "POST", {});
  const publicServer = await api("servers/community");
  assert.equal(publicServer.packVersion, published.version);
  const packResponse = await fetch(origin + published.path);
  assert.equal(packResponse.status, 200);
  const bytes = Buffer.from(await packResponse.arrayBuffer());
  assert.equal(hash(bytes), published.version);
  const pack = join(run, "community.mrpack");
  await writeFile(pack, bytes);
  console.log(`Published ${bytes.length} byte pack; starting native Compass acceptance`);
  const cargo = spawn("cargo", ["test", "--locked", "--manifest-path", join(resolve(compass), "src-tauri", "Cargo.toml"), "--test", "isles_pack_live", "--", "--ignored", "--nocapture"], { cwd: resolve(compass), env: { ...process.env, COMPASS_ISLES_PACK: pack, COMPASS_ISLES_RUN: run, COMPASS_ISLES_ORIGIN: fixtureOrigin } });
  let cargoLogs = "";
  for (const stream of [cargo.stdout, cargo.stderr]) stream.on("data", (chunk) => { cargoLogs += chunk; process.stdout.write(chunk); });
  const exit = await new Promise((done, reject) => { cargo.on("error", reject); cargo.on("exit", done); });
  await writeFile(join(run, "compass-test.log"), cargoLogs);
  assert.equal(exit, 0, "Compass install/launch acceptance failed");
  const imported = JSON.parse(await readFile(join(run, "compass-result.json"), "utf8"));
  assert(reads.filter((path) => path === "/optional.txt").length >= 3, "optional file was not fetched at publication, installation and corrupt-file verification");
  assert.equal(reads.filter((path) => path === "/server.txt").length, 1, "client fetched server-only bytes");
  const manifest = { publication: "real-isles-packwiz", contentAddressVerified: true, artifactBytes: bytes.length, ...imported };
  await writeFile(join(run, "scenario-result.json"), JSON.stringify(manifest, null, 2));
  console.log(JSON.stringify(manifest));
} finally {
  service.kill();
  fixture.close();
  await writeFile(join(run, "isles-process.log"), logs);
}
