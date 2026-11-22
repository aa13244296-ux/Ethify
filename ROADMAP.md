# Ethify roadmap

Ethify is a Windows internet manager for multi-adapter downloads and traffic. This roadmap uses text statuses so shipped work and open work are easy to scan without checkmark/cross clutter.

## Shipped in v1.0.0

- **Shipped** — Public source repository and Windows release ZIP.
- **Shipped** — Native Ethify desktop window, branding, and application icon.
- **Shipped** — Resumable HTTP downloads and torrents, plus Chromium download capture.
- **Shipped** — Per-adapter and process traffic visibility.
- **Shipped** — Optional IPv4 Wintun router with up to 512 concurrent live egress connections, per-adapter throughput/load-aware flow selection, and a short adapter retry cooldown.

## Current work

- **Working tree** — Router-specific active-flow and throughput telemetry, plus optional per-adapter TCP/UDP rate caps.
- **Working tree** — Windows desktop close-to-notification-area behavior with tray restore and explicit exit actions.

## Next

- **Next** — Collect Windows reports covering adapter combinations, Steam/CDN behavior, sleep/wake, and adapter disconnect/reconnect.
- **Next** — Improve first-run setup and troubleshooting for WebView2, administrator access, and adapter selection.
- **Next** — Publish installation and release notes with clear compatibility limits.

## Later

- **Later** — Evaluate a compatible multipath relay for splitting one transfer across links. The current local router distributes separate connections; it does not bond one TCP connection or promise equal Wi-Fi/Ethernet rates.
- **Later** — Investigate a signed Windows installer after establishing a trusted code-signing identity.

## Current constraints

- Downloads can use multiple adapters only when the server supports byte ranges and is reachable on each selected link.
- The local router balances new IPv4 flows. Existing flows stay on their selected adapter and may fail if that link disconnects.
- Steam decides its own CDN connections and may ignore proxy environment settings. Ethify cannot override a network provider's Steam access policy.
- ForceBindIP-style process binding selects one interface for a process; it does not merge multiple links for one transfer. Ethify instead distributes independent flows through its system-wide router.

## Public discovery

- **Available** — Public GitHub repository and `v1.0.0` release: <https://github.com/aa13244296-ux/Ethify/releases/tag/v1.0.0>.
- **Next** — Share a short, accurate project announcement in relevant Windows, networking, and download-manager communities. Describe supported multi-adapter downloads and the single-flow limitation; do not promise a Steam speed increase.
- **Next** — Invite users to report Windows version, adapter types, server range support, and measured per-adapter rates in GitHub Issues.

Repository publication makes Ethify discoverable, but it does not itself post announcements to social platforms or guarantee downloads.

## Design references

- [ForceBindIP](https://r1ch.net/projects/forcebindip) demonstrates explicit per-process interface binding, along with DLL-injection compatibility and DNS-routing caveats.
- [net-merge](https://github.com/net-merge/net-merge) demonstrates load-aware distribution across multiple links and adapter-level speed controls. Ethify uses these as behavioral references, not as code to copy.
