# Isles Paper bridge

Java 21 plugin for Paper/Spigot-compatible 1.21.1 servers. A real Purpur 1.21.1 build 2329 was used for the initial live scenario. Folia and other loader families are not supported by this plugin.

## Install

Build with `mvn package`, or obtain `isles-paper-bridge-0.1.0.jar` from the repository CI artifact. Put it in `plugins/` and restart once to generate `plugins/MythicIsles/config.yml`.

In the website integration manager, save the server and generate its Bridge token. Configure:

```yaml
url: https://community.example.org
server: survival
token: '<server-specific Bridge token>'
placeholders:
  rank: '%luckperms_prefix%'
```

The URL may also be a private Isles address reachable from the game host. Use TLS outside a trusted private network. This is not a Mirror token or the Isles admin token. Rotating it in the manager immediately revokes the old value.

The bridge reports every 20 seconds. Network calls run on a separate executor; Bukkit player state and placeholders are collected on the game thread. Missing optional providers do not prevent linking and whitelist operations.

## Player linking

Sign in to the website and open `/game`, choose a website player and server, then generate a code. Run `/mythic link <code>` **in the game**. A code expires in 10 minutes, is single-use, and works only on the chosen server. Game UUIDs are saved separately from website/Yggdrasil player IDs; matching names are not proof of identity.

Only the owning website session may fetch linked profile data. A game server is trusted to attest its own game UUIDs. Its token cannot manage another server or call Isles administrator routes.

## Optional providers

- **LuckPerms:** reports loaded groups and online players' primary/direct global groups; administrator can select a verified identity and grant/revoke an existing global group. This does not manage contextual permissions, expiry, tracks or game OP.
- **PlaceholderAPI:** evaluates only server-owner-configured templates; unresolved placeholders are omitted. Install the required expansions yourself. No arbitrary template endpoint is exposed to players.
- **spark:** uses its public service/API, including the server-bundled Paper service. Reports available TPS, MSPT and system CPU; absent measurements remain absent.
- **Whitelist:** idempotently sets the chosen linked UUID's whitelist membership.
- **Vault:** reads the currently registered Economy provider's balance, formatted amount and currency for an online player who already has an economy account. No account creation, transfer, deposit or withdrawal endpoint is exposed. An installed Vault without an enabled Economy provider yields no balance field. The API's numeric precision and formatted result are kept as returned by the provider.
- **ViaVersion 5:** reads server-supported protocol names after configured blocks, and the online player's actual protocol version. These are observations, not a guarantee of proxy, modpack or authentication compatibility.
- **WorldGuard 7:** reports up to 32 physical regions where the online player is an owner/member (including API-resolved inherited/group membership). Returns world and bounding-box center for navigation, never treats a global template as a physical plot. Data stays in the owner-authorized player profile and admin observation; region mutation and full polygon export are not implemented.
- **Chunky 1.4.40:** reports up to 100 loaded worlds, current/saved task selections, processed chunks, percentage and running-task rate/ETA. Administrators can start square/circle tasks (radius 1–100000 blocks, respecting the configured Chunky radius limit), pause, resume and cancel running or paused work. Start never overwrites a paused task. These controls generate chunks; they do not trim existing chunks or put the server into a login-blocking maintenance mode.
- **Plan 5 Query API:** reads stored sessions for the current Plan server UUID: recorded players, session count, duration including AFK, and AFK time. Individual rows are requested only for game UUIDs already verified on this server; website IDs and unlinked players' activity are not exported. The owning `/game` profile can read activity while offline; aggregate data is administrator-only. First/last times are stored session boundaries, not live online status. An unsaved current session is not added to stored totals.

Desired-state membership tasks persist in Isles. The bridge acknowledges applied/failed results; a lost acknowledgement safely replays the set-state operation. Pending tasks are not advertised as completed operations.

Chunky uses the same queue with a **claim-before-execute** step because start/cancel are not replayable set-state assignments. `pending` is queued; `dispatched` is claimed but has no final receipt; `applied` means the provider accepted the operation, not that generation completed. Isles persists dispatch before the game calls the provider, and never automatically replays an interrupted operation. If a claim, execution or receipt is interrupted, inspect the actual world state before issuing another operation. Task results and generation state are administrator-only.

Paused tasks are reconstructed from Chunky's own task store after a restart. Chunky 1.4.40 may save a terminal checkpoint before asynchronous callbacks finish (a quick task can save zero chunks, then emit 100% progress). The Bridge observes the actual task objects published by Chunky's lifecycle events for live completion. After restart, a terminal checkpoint that cannot establish completion versus cancellation is reported as `stopped`, not guessed as either result. ETA is only shown for a running task; completion elapsed time is never relabeled as remaining time.

Plan needs no extra Bridge configuration or web login. It uses the official `QUERY_API` capability and prepared, read-only queries through the plugin's own database service. Queries run on the background executor, with five-second statement deadlines and UUID batches of at most 500. The server-scoped allowlist refuses more than 2000 verified identities explicitly; the existing 256 KiB heartbeat budget also applies. Query failure omits Plan data from the next heartbeat while identity and membership operations continue. No synthetic zero is returned for missing statistics. Original Plan dashboard links retain Plan/reverse-proxy authentication; the Bridge does not grant dashboard access or forward its session cookies.

## Validation

```sh
mvn package
npm ci
PAPER_SERVER_JAR=/path/to/server.jar ISLES_TEST_PLUGIN_DIR=/path/to/optional-plugin-jars npm run test:live
```

The test runs a real server on loopback, connects a Mineflayer 1.21.1 client, executes the linking command, applies a whitelist task and verifies the returned profile. With LuckPerms it creates a group, grants and revokes it, and checks observed membership. With PlaceholderAPI it downloads the Server expansion and verifies actual resolution. With spark it verifies actual performance fields. The run leaves `scenario-result.json` and process logs in a printed temporary directory.

Locally validated: Purpur 1.21.1/2329, LuckPerms 5.5.71, PlaceholderAPI 2.12.3 and Purpur's bundled spark service. Optional-plugin JARs remain external test inputs; no upstream binary is redistributed in this repository.

Also validated in the same live scenario: ViaVersion 5.12.2, WorldGuard 7.0.12 and WorldEdit 7.3.9. `node test/fetch-plugins.mjs /fixture-directory` acquires these pinned optional fixtures and verifies publisher SHA-512 values. The live scenario observes the connected Mineflayer client's protocol and creates a real cuboid plot with member UUID, then checks that the linked private profile contains its center and role, while an associated global template is absent.

Vault 1.7.3 with EssentialsX Economy 2.21.2 was separately verified on the same Purpur build. The scenario sets a real fixture player's amount via the upstream economy command and observes both the raw API balance and provider formatting. `ISLES_TEST_PLUGIN_FILTER=Vault.jar,EssentialsX-2.21.2.jar` selects only those external fixtures when validating that provider, without mocking any owned module. The historical EssentialsX build reports this game build as unsupported; the observed API path passed, but this does not certify every EssentialsX feature on that server.

Select `ISLES_TEST_PLUGIN_FILTER=Chunky.jar` for the pregeneration scenario. `test/fetch-plugins.mjs` also downloads the pinned Chunky 1.4.40 artifact and verifies its publisher SHA-512. The scenario starts real square/circle tasks, observes generated chunks, pauses, restarts the game server and resumes from Chunky's stored task; it cancels running and paused work, completes a small task, and checks terminal-state uncertainty after restart. It writes `chunky-observations.json` alongside the result manifest. The Web scenario replays these provider observations at the Bridge HTTP boundary while running the production Web/Isles services.

Plan 5.9 build 3705 / SQLite was validated on Purpur 1.21.1/2329. `ISLES_TEST_PLUGIN_FILTER=Plan.jar` records a real bound player's session and an unlinked guest's session, then checks offline access, administrator totals, UUID filtering and persistence after restarting the game server. It leaves `plan-observations.json` and the normalized result manifest. The fixture acquisition script pins Modrinth version `YYpJ8C6q` and verifies publisher SHA-512. MySQL and proxy-network deployments still require their own runtime acceptance.
