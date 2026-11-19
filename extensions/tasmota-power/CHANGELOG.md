# Changelog

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
