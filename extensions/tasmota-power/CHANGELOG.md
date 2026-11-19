# Changelog

## 0.5.0 - 2026-11-19

- Add multi-plug driver support inside a single unified extension sharing safety interlocks, auto-off cooldown, 30-minute power graph, and session/print energy tracking:
  - **Shelly Gen1 (MQTT)** with `shellies/announce` discovery, online LWT, overpower detection, and per-relay power readings.
  - **Shelly Gen2 / Plus / Pro (MQTT)** (`<prefix>/command/switch:<id>` + `<prefix>/status/switch:<id>` JSON telemetry).
  - **Shelly Gen1 & Gen2+ (Direct HTTP)** using Gen2+ RPC (`/rpc/Switch.GetStatus`, `/rpc/Switch.Set`) with automatic fallback to Gen1 REST (`/status`, `/relay/<i>?turn=on|off`).
  - **Zigbee2MQTT** (`<topic>/set`, `<topic>/get`, string & JSON availability, multi-endpoint `state_l<n>`, and power/voltage/current telemetry).
  - **ESPHome (MQTT)** (`<prefix>/command`, `<prefix>/state`, `<node>/status`).
  - **OpenBeken (MQTT)** (`<name>/<ch>/set`, `<name>/<ch>/get`, `<name>/connected`, power/voltage/current).
  - **Generic MQTT** with custom command/state/availability/power topics, custom ON/OFF payloads, and optional JSON key extraction.
  - **Home Assistant (REST)** optional integration (`ha_url` + Long-Lived Access Token, live `POST /ha/test` entity picker for switch + optional power sensor) for TP-Link Kasa/Tapo, Matter, Thread, Z-Wave, and Tuya plugs.
- Split the extension's settings into three tabs: **Plugs** (every printer with its plug and live state), **Connections** (MQTT broker and Home Assistant, each tested and saved on its own) and **Automation** (the power-off rules, and each printer's auto-off choice and own delay).
- Set up a plug in a guided dialog: pick the plug, pick how Connect reaches it, then fill in only the fields that plug needs. The dialog waits for the plug and says whether it answered. A plug and relay another printer already has is refused before saving.
- Add a **Power** page in the sidebar with what the fleet draws now, which plugs are on, and buttons that switch each plug.
- Add a **Printer power** dashboard card.
- Rebuild the printer's **Power Control** section and the listing card line on the app's own components, in light and dark theme. A plug that does not answer is now said on the card instead of leaving the line empty.
- Read every plug in one request (`GET /overview`), kept to the printers the caller may see, in place of one request per printer card.
- Declare the built-in MQTT broker's port (`needs.listen`, TCP 1883). **The built-in broker now starts only on a port you approved**: after updating, approve it on the extension's Ports tab if you use the built-in broker. The port can be moved there.
- Fix a start failure when `http_host` was written on the extension's host Settings tab. A value written there for `mqtt_host` or `ha_url` now fixes that value, and the Connections tab shows it as read-only.
- Fix a new install starting with an auto-off delay of 0 seconds instead of the default 120.
- Preserve full backwards compatibility with existing `mode: "mqtt"` and `mode: "http"` printer assignments.
- Add unit tests in `drivers_test.go` covering all new plug drivers and legacy config compatibility, and in `host_test.go` covering the declared options, the granted port and the overview.
- Hold the browser bundle to the app's own lint and formatting rules (`pnpm run check`).

## 0.4.1 - 2026-11-19

- Hardwire the optional built-in MQTT 3.1.1 broker to TCP port 1883 and warn when an existing MQTT broker or missing Docker `-p 1883:1883` port mapping is detected.
- Fix **Test Connection** button state when built-in broker mode is active with an empty external host field.
- Fix dark mode error alert text contrast, prevent German/locale thousands separators on port numbers in status badges, and preserve unsaved printer mode drafts during plug scans.
- Declare `require.core`, `options`, and `egress` in `module.yaml` and prepare standalone Marketplace build metadata.

## 0.4.0 - 2026-11-19

- Add optional built-in pure-Go MQTT 3.1/3.1.1 broker (`mqtt_builtin`) in Settings so Tasmota plugs can connect directly without running an external Mosquitto broker.
- Add per-printer Direct HTTP plug connection mode (`mode: "http"`, `http_host`, optional Tasmota Web Password) polling `/cm?cmnd=Status 0` every 5s and switching via verified `POWER<n>` HTTP commands without needing MQTT at all.
- Add end-to-end Go tests (`TestEmbeddedMQTTBrokerEndToEnd` and `TestDirectHTTPTasmotaPollAndSwitch`).

## 0.3.2 - 2026-11-19

- Remove the duplicate sampling interval badge above the power usage graph while keeping the explanatory caption below the chart.
- Add full multi-platform release builds (`linux_amd64`, `linux_arm64`, `darwin_amd64`, `darwin_arm64`, `windows_amd64`), MIT `LICENSE`, and Prusa shape/checksum verification in CI.

## 0.3.1 - 2026-11-19

- Replace the inline Turn Off confirmation bar with Prusa Connect's native `AlertDialog` modal.
- Display the smart plug power telemetry sampling step (`5s`) on the power usage graph and expose `poll_interval_sec` in the device status API.

## 0.3.0 - 2026-11-19

- Support multi-relay Tasmota devices and `SetOption26 1` (`POWER1`..`POWER8`) with a Relay selector in Settings and per-relay double-assignment locking.
- Send `"1"`/`"0"` commands with QoS 1 (never retained) and honor custom `StateText1..4` and LWT `onln`/`ofln` values from Tasmota discovery.
- Parse JSON and plain-text payloads across `RESULT`, `STATE`, `POWER<n>`, `STATUS8`, and `STATUS10` (`SetOption4 1` and `SetOption90 1`), while ignoring empty `PowerRetain 0` clear payloads.
- Support custom `FullTopic` and `Prefix1..3` layouts from Tasmota discovery and ignore stale `LWT` messages from superseded topic patterns.
- Verify power switch replies within 2.5s (`PowerLock`, `PowerOnState 4`, or invalid relay errors) and display an explicit `Offline` state when LWT is `Offline`.
- Add Go unit tests (`main_test.go`) covering all Tasmota MQTT variants and run them in the extension release workflow.

## 0.2.3 - 2026-11-19

- Fix power usage graph aspect-ratio distortion by moving axis labels and the live endpoint dot to HTML and using `vector-effect="non-scaling-stroke"` with a `100 W` minimum Y-axis scale.
- Add a confirmation step to the **Turn Off** button on the printer page, configurable in **Settings** (`confirm_turn_off`, enabled by default).

## 0.2.2 - 2026-11-18

- Poll assigned Tasmota plugs every 5 seconds via `Status 8` for responsive energy telemetry.
- Add a live SVG power usage graph (`W` over time) to each printer's **Tasmota Power Control** section.
- Track total energy (`Wh`/`kWh`), duration, average power, and peak power while the printer's smart plug is turned on.
- Track per-print file energy usage (`current_print_usage`) until both printing and post-print axis movement stop, and keep the last 10 completed prints (`print_history`) persisted in `usage.json`.

## 0.2.1 - 2026-11-18

- Show connection test and save results in green on success and red on failure.
- Clarify the Auto-off delay after movement stops field and Client ID with helpful descriptions.
- Redesign the Printer Plug Assignments list with explicit Save Assignment button and saved confirmation badge.
- Remove the redundant Toggle Power action from the printer header menu.
- Detect axis movement (`/axis/x`, `/axis/y`, `/axis/z`) so power-off waits until the printer stops moving after a print finishes.
- Log printer human-readable names alongside IDs under `tasmota-power-control`.

## 0.2.0 - 2026-11-18

- Rename extension title to **Tasmota Power Control**.
- Unify extension configuration into a single **Settings** tab with separate Broker Host, Port, and Use TLS controls, inline Username/Password fields, and a live **Test Connection** action.
- Centralize printer-to-Tasmota plug assignment in Settings with double-assignment locking.
- Redesign the printer **Power Control** card using Prusa Connect UI primitives and remove configuration inputs from the printer page.

## 0.1.0 - 2026-11-17

- Initial release with Tasmota MQTT power control, live plug status/power monitoring, active-print safety interlock, and auto-off after print.
