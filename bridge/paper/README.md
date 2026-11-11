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
- **spark:** uses its public service/API, including the server-bundled Paper service. Reports available TPS, MSPT, system CPU and administrator-only GC collection counts/time per collector. GC counters use spark's normal-operation baseline, excluding startup activity; values reset with the provider/server, not a recent interval or pause histogram. Unknown counters are individually omitted rather than changed to zero. No GC control endpoint is exposed.
- **Whitelist:** idempotently sets the chosen linked UUID's whitelist membership.
- **Vault:** reads the currently registered Economy provider's balance, formatted amount and currency for an online player who already has an economy account. No account creation, transfer, deposit or withdrawal endpoint is exposed. An installed Vault without an enabled Economy provider yields no balance field. The API's numeric precision and formatted result are kept as returned by the provider.
- **ViaVersion 5 / ViaBackwards / ViaRewind:** reads server-supported protocol names from ViaVersion after configured blocks and installed extension paths, and the online player's actual protocol version. Installed extension versions appear in the administrator plugin inventory. These are observations, not a guarantee of full gameplay, proxy, modpack or authentication compatibility. No separate handwritten version range is maintained.
- **WorldGuard 7:** reports up to 32 physical regions where the online player is an owner/member (including API-resolved inherited/group membership). Returns world and bounding-box center for navigation, never treats a global template as a physical plot. Data stays in the owner-authorized player profile and admin observation; region mutation and full polygon export are not implemented.
- **Chunky 1.4.40:** reports up to 100 loaded worlds, current/saved task selections, processed chunks, percentage and running-task rate/ETA. Administrators can start square/circle tasks (radius 1–100000 blocks, respecting the configured Chunky radius limit), pause, resume and cancel running or paused work. Start never overwrites a paused task. These controls generate chunks; they do not trim existing chunks or put the server into a login-blocking maintenance mode.
- **Plan 5 Query API:** reads stored sessions for the current Plan server UUID: recorded players, session count, duration including AFK, and AFK time. Individual rows are requested only for game UUIDs already verified on this server; website IDs and unlinked players' activity are not exported. The owning `/game` profile can read activity while offline; aggregate data is administrator-only. First/last times are stored session boundaries, not live online status. An unsaved current session is not added to stored totals.
- **Towny 0.103:** reports administrator-only towns/nations (UUID, name, counts, open join/public spawn flags) and verified residents' own town/nation, mayor/king roles and provider rank names. Reads the API on the Bukkit thread, including offline residents. A known townless resident is distinct from missing/unavailable provider data. `public` is a public-spawn flag, not permission to expose website records. Townblock counts are not converted to assumed square-block area. No spawn coordinates, resident directory, balances, invites or mutation endpoint is exported.
- **Geyser/Floodgate:** queries Floodgate's online-player API first, then local Geyser connections; never classifies by name prefix or UUID pattern. A game-code binding records `bedrock` when actually observed by either provider; otherwise the Bukkit Java connection remains `java`. Private online observations include raw Bedrock version, provider and provider-side Java-account link state. That state is distinct from website binding. Membership continues targeting Bukkit's actual UUID, including Floodgate's linked-Java mode; it is not recalculated from XUID. A local Geyser also reports its supported Bedrock versions and Java translation target for administrators. No Xbox ID, device, IP or session token is collected.

Desired-state membership tasks persist in Isles. The bridge acknowledges applied/failed results; a lost acknowledgement safely replays the set-state operation. Pending tasks are not advertised as completed operations.

Chunky uses the same queue with a **claim-before-execute** step because start/cancel are not replayable set-state assignments. `pending` is queued; `dispatched` is claimed but has no final receipt; `applied` means the provider accepted the operation, not that generation completed. Isles persists dispatch before the game calls the provider, and never automatically replays an interrupted operation. If a claim, execution or receipt is interrupted, inspect the actual world state before issuing another operation. Task results and generation state are administrator-only.

Paused tasks are reconstructed from Chunky's own task store after a restart. Chunky 1.4.40 may save a terminal checkpoint before asynchronous callbacks finish (a quick task can save zero chunks, then emit 100% progress). The Bridge observes the actual task objects published by Chunky's lifecycle events for live completion. After restart, a terminal checkpoint that cannot establish completion versus cancellation is reported as `stopped`, not guessed as either result. ETA is only shown for a running task; completion elapsed time is never relabeled as remaining time.

Plan needs no extra Bridge configuration or web login. It uses the official `QUERY_API` capability and prepared, read-only queries through the plugin's own database service. Queries run on the background executor, with five-second statement deadlines and UUID batches of at most 500. The server-scoped allowlist refuses more than 2000 verified identities explicitly; the existing 256 KiB heartbeat budget also applies. Query failure omits Plan data from the next heartbeat while identity and membership operations continue. No synthetic zero is returned for missing statistics. Original Plan dashboard links retain Plan/reverse-proxy authentication; the Bridge does not grant dashboard access or forward its session cookies.

Towny shares the same verified-UUID allowlist. Its snapshots retain up to 100 towns and 100 nations ordered by UUID; full provider counts accompany the lists so the administrator sees when entries were omitted. An owning player's town is included independently of that list limit. A provider read failure omits Towny while other observations continue. The 2000-identity and 256 KiB heartbeat limits also apply; more than 32 ranks on one resident explicitly makes that Towny collection unavailable rather than silently dropping roles. Towny itself handles storage, economy, permissions and all game-side changes.

Crossplay observations do not set up Geyser, open UDP ports, change authentication or link provider accounts. Install/configure the providers using their official guides, then restart the Bridge. Bedrock users generate the code for the backend server ID where this Bridge is installed. A separately published Bedrock directory/probe entry is not automatically a second Bridge identity. The binding's platform records the connection used at bind time; current provider details exist only while observed online. ViaVersion's Java-facing protocol for a Bedrock connection is labeled separately from its raw Bedrock version. Xbox authentication, Java-account linking and proxy data forwarding still need their own deployment acceptance.

## Validation

```sh
mvn package
npm ci
PAPER_SERVER_JAR=/path/to/server.jar ISLES_TEST_PLUGIN_DIR=/path/to/optional-plugin-jars npm run test:live
```

The test runs a real server on loopback, connects a Mineflayer 1.21.1 client, executes the linking command, applies a whitelist task and verifies the returned profile. With LuckPerms it creates a group, grants and revokes it, and checks observed membership. With PlaceholderAPI it downloads the Server expansion and verifies actual resolution. With spark it verifies performance fields, uses JDK `jcmd` (required on PATH) to request `GC.run` on the owned server process, and checks counter growth in the next real heartbeat. It retains `gc-observations.json`, `scenario-result.json` and process logs in a printed temporary directory. `ISLES_TEST_DISABLE_SPARK=1` disables Paper's bundled spark in the temporary fixture config to exercise the absent-provider path; it is not a Bridge deployment setting.

Locally validated: Purpur 1.21.1/2329, LuckPerms 5.5.71, PlaceholderAPI 2.12.3 and Purpur's bundled spark service. Optional-plugin JARs remain external test inputs; no upstream binary is redistributed in this repository.

Also validated in the same live scenario: ViaVersion 5.12.2, WorldGuard 7.0.12 and WorldEdit 7.3.9. `node test/fetch-plugins.mjs /fixture-directory` acquires these pinned optional fixtures and verifies publisher SHA-512 values. The live scenario observes the connected Mineflayer client's protocol and creates a real cuboid plot with member UUID, then checks that the linked private profile contains its center and role, while an associated global template is absent.

Select `ISLES_TEST_PLUGIN_FILTER=ViaVersion.jar,ViaBackwards.jar,ViaRewind.jar` for older Java clients. The pinned builds are ViaVersion/ViaBackwards 5.12.2 and ViaRewind 4.2.0 (Modrinth `1xHfYAKG` / `EPLCoxMK` for the extensions). The real 1.12.2 and 1.8.8 clients spawn and bind their protocol-login UUIDs, then the scenario restarts with upstream `block-protocols: [47]` and verifies both removal of `1.8.x` from the provider list and an explicit rejection of a new 1.8.8 connection. It writes `legacy-protocol-observations.json`. Without ViaRewind it validates 1.12.2 separately; with ViaVersion alone it checks that 1.12 is not advertised and cannot spawn. These runs are offline-mode loopback protocol fixtures, not online authentication or full historical gameplay acceptance. ViaBackwards warns that old clients cannot represent the modern world-height range; 1.7 clients, proxy forwarding and Compass preflight remain separate work.

Vault 1.7.3 with EssentialsX Economy 2.21.2 was separately verified on the same Purpur build. The scenario sets a real fixture player's amount via the upstream economy command and observes both the raw API balance and provider formatting. `ISLES_TEST_PLUGIN_FILTER=Vault.jar,EssentialsX-2.21.2.jar` selects only those external fixtures when validating that provider, without mocking any owned module. The historical EssentialsX build reports this game build as unsupported; the observed API path passed, but this does not certify every EssentialsX feature on that server.

Select `ISLES_TEST_PLUGIN_FILTER=Chunky.jar` for the pregeneration scenario. `test/fetch-plugins.mjs` also downloads the pinned Chunky 1.4.40 artifact and verifies its publisher SHA-512. The scenario starts real square/circle tasks, observes generated chunks, pauses, restarts the game server and resumes from Chunky's stored task; it cancels running and paused work, completes a small task, and checks terminal-state uncertainty after restart. It writes `chunky-observations.json` alongside the result manifest. The Web scenario replays these provider observations at the Bridge HTTP boundary while running the production Web/Isles services.

Plan 5.9 build 3705 / SQLite was validated on Purpur 1.21.1/2329. `ISLES_TEST_PLUGIN_FILTER=Plan.jar` records a real bound player's session and an unlinked guest's session, then checks offline access, administrator totals, UUID filtering and persistence after restarting the game server. It leaves `plan-observations.json` and the normalized result manifest. The fixture acquisition script pins Modrinth version `YYpJ8C6q` and verifies publisher SHA-512. MySQL and proxy-network deployments still require their own runtime acceptance.

Towny 0.103.2.0 / flatfile was validated on Purpur 1.21.1/2329. Select `ISLES_TEST_PLUGIN_FILTER=Towny.jar`: the scenario creates a town/nation via the real plugin commands, observes mayor/king, adds an unlinked guest, transfers leadership, grants assistant rank, restarts and verifies offline membership, then removes the resident and verifies the townless state. `towny-observations.json` retains those actual observations. The fixture script pins Modrinth `pra46LOM` and checks publisher SHA-512; no Towny binary is redistributed. Towny's missing-economy and `TownMobPVM:SulfurCube` warnings on this historical game build do not prevent the tested read path; economy/war and other Towny features are not certified by this scenario. MySQL, Folia, public recruitment and map overlays remain separate work.

Crossplay protocol acceptance used Geyser 2.8.3 b941 (Modrinth `nYyvuBsj`), Floodgate 2.2.5 b141 (official download API SHA-256 pinned), ViaVersion 5.12.2, Purpur 1.21.1/2329 and bedrock-protocol 3.49.0 / Bedrock 1.21.100. Run `npm ci`, then select `ISLES_TEST_PLUGIN_FILTER=Geyser-Spigot.jar,floodgate-spigot.jar,ViaVersion.jar`. The headless client receives `start_game`, initializes and executes the actual game binding command; the scenario checks private metadata, the native whitelist file's Bukkit UUID and the real UDP RakNet probe. Geyser without Floodgate is separately covered with `Geyser-Spigot.jar,ViaVersion.jar`. `crossplay-observations.json` and the result manifest are retained.

These are **unsigned loopback protocol fixtures**, with `enable-proxy-connections` enabled only in the temporary fixture config; the Floodgate fixture's XUID is zero. They exercise actual plugin/protocol handling, not Xbox Live authentication or a real provider-linked Java account. Keep normal provider authentication in a public deployment; authenticated Xbox sessions, provider account-linking and proxy forwarding remain runtime follow-ups. The compile dependencies exclude Geyser's bundled `common` runtime jar because it embeds an old unrelocated Gson; the installed providers own that runtime, and Bridge retains its own relocated Gson.
