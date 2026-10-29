# ToofanWrt

Router panel for OpenWrt 23.05 (first target: Google WiFi AC-1304, ipq40xx, armv7): Xray-based
free internet (VLESS/VMess/Trojan/SS/WireGuard/OpenVPN) with Iran split, devices, Wi-Fi, DNS,
gaming, LED. Replaces LuCI as the router's only web UI (port 80). The owner writes Persian:
reply in Persian.

## Layout
- `backend/` Go, stdlib only, module `toofanwrt`. `main.go` flags: `-listen -config -xray -web -mock -version`.
  `internal/`: `api` (HTTP routes), `core` (wires everything, firewall/DNS apply loop, diagnostics,
  speed test), `proxy` (Xray config, watchdog, server tester, links/subs), `fw` (nftables),
  `dnsctl` (dnsmasq drop-in), `devices`, `wifi`, `gaming`, `led`, `luci`, `update`, `sitelog`,
  `config` (JSON store at /etc/toofanwrt/config.json), `uci`, `env`.
- `frontend/` Preact + Vite + TS; pages in `src/pages/`, shared UI in `src/ui.tsx`, i18n `L(fa, en)`.
  Built into `backend/web/dist` (embedded).
- `openwrt/` `install.sh` (also migrates the old "Separ" install), `uninstall.sh`, init/hotplug files,
  offline package lists. `build.sh` builds a release tarball into `dist/`.

## Commands
- Tests: `cd backend && go vet ./... && go test ./...` (Xray config check: `XRAY_BIN=… XRAY_ASSETS=… go test ./internal/proxy`;
  nft syntax: `NFT_CHECK=1 go test ./internal/fw`).
- Frontend: `cd frontend && npx tsc --noEmit -p .`
- Mock panel: `cd backend && go run . -mock -listen 127.0.0.1:18080` (password `admin`).
- Release: `./build.sh` (needs `openwrt/pkgs/`, not in git).

## Conventions
- UCI section names `separ_guest*`, `separ_ssid_*`, `separ_uplink` are kept on purpose (they exist on routers).
- The dnsmasq drop-in must never repeat single-value options OpenWrt sets (cache-size, log-async…): dnsmasq
  refuses to start and the whole LAN loses DNS (see `dnsctl` test).
- Commit/push only when asked; end commit messages with the attribution lines from the session.

## Using graphify here
Query by symbol or file names, not sentences: `graphify explain "SetStaticIP"`, `graphify query "dnsctl Apply"`,
`graphify path "proxySettings" "nft.go::Render()"`. Ambiguous names: use `file.go::Symbol`.

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

Rules:
- When graphify-out/graph.json exists, run `graphify query "<question>"` before your first grep, search or source-file read for a codebase question, and put this rule in every subagent prompt that explores code. Read raw files only for lines the graph or an earlier read already located. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
- A committed graphify-out/graph.json is rewritten whole on every build, so a large diff to it in a pull request is expected. Do not flag it as churn or suggest skipping the graph rebuild or its hooks.
