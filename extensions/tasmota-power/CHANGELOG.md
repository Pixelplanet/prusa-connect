# Changelog

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
