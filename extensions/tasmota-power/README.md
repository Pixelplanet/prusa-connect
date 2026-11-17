# Tasmota Power

Monitors and controls Tasmota smart plugs for printers over MQTT.

## Features

- **Power On / Off** button on the printer's Status page (`device_section`) and header actions (`device_action`).
- **Live Status & Power Draw** (`ON` / `OFF` / `Offline` and wattage from `tele/<topic>/SENSOR`) on both the printer page and fleet cards (`device_card`).
- **Active-Print Safety Interlock**: Refuses turning off the plug while the printer is `PRINTING`, `PAUSED`, `BUSY`, `ATTENTION`, or has an active print job.
- **Turn Off When Print Is Done**: One-shot auto-off toggle per printer with configurable cooldown delay (`auto_off_cooldown_sec`, default 120s).

## Gates

```sh
make check
```
