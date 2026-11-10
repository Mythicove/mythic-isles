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
await writeFile(join(dir, "optional-fixtures.json"), JSON.stringify(manifest, null, 2));
console.log(JSON.stringify(manifest));
