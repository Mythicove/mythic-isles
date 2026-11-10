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
- **ViaVersion 5:** reads server-supported protocol names after configured blocks, and the online player's actual protocol version. These are observations, not a guarantee of proxy, modpack or authentication compatibility.
- **WorldGuard 7:** reports up to 32 physical regions where the online player is an owner/member (including API-resolved inherited/group membership). Returns world and bounding-box center for navigation, never treats a global template as a physical plot. Data stays in the owner-authorized player profile and admin observation; region mutation and full polygon export are not implemented.

Desired-state membership tasks persist in Isles. The bridge acknowledges applied/failed results; a lost acknowledgement safely replays the set-state operation. Pending tasks are not advertised as completed operations.

## Validation

```sh
mvn package
npm ci
PAPER_SERVER_JAR=/path/to/server.jar ISLES_TEST_PLUGIN_DIR=/path/to/optional-plugin-jars npm run test:live
```

The test runs a real server on loopback, connects a Mineflayer 1.21.1 client, executes the linking command, applies a whitelist task and verifies the returned profile. With LuckPerms it creates a group, grants and revokes it, and checks observed membership. With PlaceholderAPI it downloads the Server expansion and verifies actual resolution. With spark it verifies actual performance fields. The run leaves `scenario-result.json` and process logs in a printed temporary directory.

Locally validated: Purpur 1.21.1/2329, LuckPerms 5.5.71, PlaceholderAPI 2.12.3 and Purpur's bundled spark service. Optional-plugin JARs remain external test inputs; no upstream binary is redistributed in this repository.

Also validated in the same live scenario: ViaVersion 5.12.2, WorldGuard 7.0.12 and WorldEdit 7.3.9. `node test/fetch-plugins.mjs /fixture-directory` acquires these pinned optional fixtures and verifies publisher SHA-512 values. The live scenario observes the connected Mineflayer client's protocol and creates a real cuboid plot with member UUID, then checks that the linked private profile contains its center and role, while an associated global template is absent.
