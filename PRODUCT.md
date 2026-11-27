# Product

<!-- impeccable:product-schema 1 -->

## Platform

Windows desktop application

## Users

Windows users who want to manage downloads and route traffic across Wi-Fi and Ethernet, with clear visibility into which adapters and processes are using bandwidth.

## Product Purpose

Ethify is an internet manager for Windows. It can accelerate supported HTTP downloads by fetching separate byte ranges over multiple network adapters, manage resumable downloads and torrents, and route separate connections across adapters. Its per-connection router does not split a single arbitrary TCP/UDP connection; true multipath bonding requires a compatible remote relay.

## Positioning

Ethify focuses on practical controls and transparent network behavior: show measured traffic by adapter and process, use multiple links when the transfer or application supports it, and explain when a feature distributes separate connections rather than combining one connection's bandwidth.

## Operating Context

Ethify runs as a native Windows desktop window containing its local web dashboard. The Go client hosts the dashboard on `127.0.0.1:18889`; the WebView2 runtime renders it. System-wide routing requires elevation and is off unless deliberately enabled.

## Capabilities and Constraints

- Multi-adapter HTTP(S) downloads using parallel byte-range requests when the server supports ranges and is reachable over the selected adapters.
- Pause, resume, and persisted download state for supported HTTP downloads and torrents.
- A local multi-adapter proxy that distributes separate TCP connections across selected adapters.
- Per-adapter and process-level traffic measurements from Windows network counters.
- An optional Windows IPv4 router that distributes separate outbound flows across active physical adapters.
- Chromium browser-extension support for handing supported downloads to Ethify.
- A Windows WebView2 desktop window with Ethify name and icon; Windows WebView2 Runtime is required.
- No claim that a single arbitrary connection (such as one Steam transfer) is bonded across links; a compatible multipath relay is required for that behavior.

## Brand Commitments

- **Brand Name**: Ethify
- **Brand Mark**: A clean E-shaped network glyph in cyan and green on a deep navy tile, used consistently in the application and Windows icon.
- **Aesthetic Direction**: Calm, readable dark interface with clear status and measured network information.

## Evidence on Hand

- Ethify application artwork and Windows icon in `assets/`.
- Windows desktop window and application manifest in `cmd/ethify-client/` and `assets/`.
- Local dashboard endpoints for status, downloads, torrents, traffic, adapters, and routing.

## Product Principles

1. **Evidence Over Marketing**: Show measured transfer and adapter rates; do not imply that separate connections are one bonded connection.
2. **User Control**: Keep system-wide routing opt-in and explain its administrator requirement.
3. **Reliable Recovery**: Preserve supported download progress across pauses, exits, and restarts.
4. **Clear Identity**: Use Ethify as the product name and logo; retain legacy repository/module identifiers only where changing them would cause broad compatibility churn.
