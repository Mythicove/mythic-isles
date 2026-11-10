// Opt-in fixture acquisition from pinned official Modrinth versions. Binaries
// stay in the caller-selected directory and are not redistributed in the repo.
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { createHash } from "node:crypto";
import assert from "node:assert/strict";

const destination = process.argv[2];
if (!destination) throw new Error("Usage: node test/fetch-plugins.mjs /absolute/fixture-directory");
const dir = resolve(destination);
await mkdir(dir, { recursive: true });
const versions = [
  { project: "simple-voice-chat", version: "5OqsnSs7", name: "voicechat.jar" },
  { project: "geyser", version: "nYyvuBsj", name: "Geyser-Spigot.jar" },
  { project: "towny", version: "pra46LOM", name: "Towny.jar" },
  { project: "plan", version: "YYpJ8C6q", name: "Plan.jar" },
  { project: "chunky", version: "P3y2MXnd", name: "Chunky.jar" },
  { project: "worldguard", version: "J66QOTLZ", name: "WorldGuard.jar" },
  { project: "worldedit", version: "Bu1zaaoc", name: "WorldEdit.jar" },
  { project: "viaversion", version: "Fkfcr89K", name: "ViaVersion.jar" },
];
const manifest = [];
for (const entry of versions) {
  const res = await fetch(`https://api.modrinth.com/v2/version/${entry.version}`, { headers: { "User-Agent": "Mythicove/Isles-live-tests (https://github.com/Mythicove/mythic-isles)" }, signal: AbortSignal.timeout(30_000) });
  assert(res.ok, `Version request failed: ${res.status}`);
  const version = await res.json();
  const file = version.files.find((f) => f.primary) ?? version.files[0];
  const artifact = await fetch(file.url, { signal: AbortSignal.timeout(120_000) });
  assert(artifact.ok, `Artifact request failed: ${artifact.status}`);
  const bytes = Buffer.from(await artifact.arrayBuffer());
  const hash = createHash("sha512").update(bytes).digest("hex");
  assert.equal(hash, file.hashes.sha512, "Downloaded fixture does not match publisher hash");
  await writeFile(join(dir, entry.name), bytes);
  manifest.push({ ...entry, publisherVersion: version.version_number, sha512: hash, url: file.url });
}
// Floodgate's Bukkit build is published through the official Geyser download API.
const floodgate = { project: "floodgate", version: "2.2.5", build: 141, name: "floodgate-spigot.jar" };
const floodgateURL = `https://download.geysermc.org/v2/projects/floodgate/versions/${floodgate.version}/builds/${floodgate.build}/downloads/spigot`;
const artifact = await fetch(floodgateURL, { signal: AbortSignal.timeout(120_000) });
assert(artifact.ok, `Floodgate artifact request failed: ${artifact.status}`);
const bytes = Buffer.from(await artifact.arrayBuffer());
const sha256 = createHash("sha256").update(bytes).digest("hex");
assert.equal(sha256, "21570aff9ce17d6983928e8552777760e1ede5050026b04c686b0ae112e6fd7e");
await writeFile(join(dir, floodgate.name), bytes);
manifest.push({ ...floodgate, sha256, url: floodgateURL });
await writeFile(join(dir, "optional-fixtures.json"), JSON.stringify(manifest, null, 2));
console.log(JSON.stringify(manifest));
