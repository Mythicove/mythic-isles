# Mythic Isles

Optional self-hosted Minecraft server directory and integration service for Mythicove.

## Implemented

- Persistent published/draft servers with Java or Bedrock connection addresses.
- Java SLP and Bedrock RakNet status, shared 20-second observations with timestamps.
- MCSManager: discover instances, runtime, start/stop/restart, commands, bounded recent logs.
- Pterodactyl Client API: discover servers, resource status, power and commands.
- Prometheus exporter: selected server-level metrics through the authenticated admin API.
- packwiz 1.1 source publication to content-addressed `.mrpack`: verifies index, metadata and file hashes; preserves sides, optional environments and overrides.
- Connection secrets encrypted with AES-256-GCM using a deployment key kept outside the data directory. Tokens are never returned by configuration APIs.
- Paper-compatible Java 21 Bridge: one-time verified game identity linking, owner-only profile data, durable whitelist and global LuckPerms membership tasks, optional PlaceholderAPI and spark observations. See `bridge/paper/README.md` for tested versions and live scenario.

These adapters have HTTP/TCP/UDP protocol scenario coverage. Installed panel/plugin versions still need deployment-specific verification. Pterodactyl's WebSocket console and MCSManager SSO/live streams are not implemented here yet.

## Run

Requires Go 1.26+. Generate independent random values for `ISLES_ADMIN_TOKEN` (32+ characters) and `ISLES_SECRET_KEY` (32 random bytes encoded as base64).

```sh
export ISLES_ADMIN_TOKEN='<random-service-token>'
export ISLES_SECRET_KEY='<base64-32-byte-key>'
export ISLES_DATA_DIR='./data'
export ISLES_LISTEN='127.0.0.1:8095'
go run ./cmd/isles
```

Run one process per data directory. Keep the encryption key when restoring backups: losing it makes stored credentials unreadable. Back up the data directory and keep a separate protected copy of the deployment secrets. The service creates its files with owner-only permissions on platforms that support them.

The Web BFF authenticates Mirror administrators before making service-token calls. Do not put `ISLES_ADMIN_TOKEN` in browser settings, public environment variables, or game-client configuration. Bind to loopback/private container networking. Public routes are `/api/isles/servers` and `/api/isles/packs/...`; `/api/isles/admin/...` always requires `Authorization: Bearer <ISLES_ADMIN_TOKEN>`.

Example administration:

```sh
curl -X PUT http://127.0.0.1:8095/api/isles/admin/servers/survival \
  -H "Authorization: Bearer $ISLES_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Survival","edition":"java","address":"mc.example.org:25565","published":true,"links":[]}'
```

Connection URLs are administrator-controlled and may target private panel networks. Public users can only probe configured, published addresses. Server process state and game reachability remain separate. Probe latency is measured from this service, not from the player.

## packwiz publication

Set a server's `packwizUrl`, then POST `/api/isles/admin/servers/{id}/publish-pack`. The returned version is the SHA-256 of the ZIP. Downloads are available only when attached to a published server; failed publication never replaces the selected release.

Limits: 90 seconds, 256 MiB fetched per publication, 128 MiB per remote mod, 32 MiB per override, 2000 indexed entries. SHA-1/SHA-256/SHA-512/MD5 source verification is supported. Metadata-only CurseForge mode, Murmur2 and index aliases require upstream packwiz export and are explicitly rejected. Pack URLs do not carry panel credentials. Files are fetched to verify them and derive both `.mrpack` hashes; external mod binaries remain upstream downloads rather than being mirrored into the ZIP. The `.mrpack` optional environment does not preserve packwiz's default-selection UI.

## Verification

```sh
go test ./...
go vet ./...
```

HTTP scenarios use the production server assembly, an HTTP panel boundary, a hash-verified pack source, and TCP/UDP game status boundaries. They cover publication integrity, restart persistence, secret isolation and authorization. No Minecraft plugin support is claimed based on these panel tests.

## License

Elastic License 2.0, consistent with the other Mythicove products. Third-party services and content retain their respective licenses.
