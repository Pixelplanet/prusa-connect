# Tasmota Power Control

Monitors and controls Tasmota smart plugs for printers over MQTT inside Prusa Connect Local.

## Features

- **Unified MQTT Settings & Connection Test**: Configure broker host, port, TLS, credentials, client ID, cooldown delay, and Turn Off confirmation preference with an inline connection test.
- **Auto-Discovery & Multi-Relay Support (`POWER1`–`POWER8`)**: Discovers Tasmota plugs via `tasmota/discovery/+/config` (with fallback manual topic input) and prevents accidentally assigning the same plug/relay to multiple printers.
- **Broad Tasmota Compatibility**: Handles `SetOption4 1`, `SetOption26 1`, `SetOption90 1`, custom `StateText1..4`, custom `FullTopic` / `Prefix1..3`, empty `PowerRetain 0` clear payloads, `Status 8`/`10`/`11`, stale `LWT` protection, and verified switching (`PowerLock` / invalid relay detection).
- **Active-Print & Axis Motion Safety Interlock**: Blocks power-off while printing, paused, busy, or while printer axes (`/axis/x`, `/axis/y`, `/axis/z`) are still moving.
- **Auto-Off After Print**: Optional one-shot auto-off switch per printer that waits until the job finishes and movement settles plus the configured cooldown delay.
- **Configurable Turn-Off Confirmation**: Optional native Prusa Connect `AlertDialog` confirmation before cutting power manually.
- **Live Power Graph & Energy Tracking**: 5-second active `Status 8` polling, 30-minute rolling wattage graph, current/last turn-on session totals (`Wh`/`kWh`, duration, average/peak `W`), and per-print file energy history persisted to `usage.json`.

## Gates & Release Packaging

```sh
# Validate shape & run unit/Svelte checks
sh extensions/scripts/check-shape.sh tasmota-power
make -C extensions/tasmota-power check

# Build multi-platform release archive with checksums.txt
(cd extensions/tasmota-power/ui && pnpm install && pnpm run build)
sh extensions/scripts/build-release.sh extensions/tasmota-power bin/release/tasmota-power
```
