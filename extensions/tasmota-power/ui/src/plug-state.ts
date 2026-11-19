// The one answer every screen gives to "what is this printer's plug doing".
// A plug that is off, a plug nobody can reach and a plug nothing was heard
// from yet are three different facts, and a printer with no plug is a fourth.
import type { BadgeVariant } from '@prusa/connect-ui/primitives';

import {
  type DeviceConfig,
  isDeviceConfigured,
  type PlugTelemetry,
  readingOf,
} from './api';

export type PlugStateName = 'none' | 'on' | 'off' | 'offline' | 'unknown';

export const plugStateOf = (
  config: DeviceConfig | undefined,
  plug: PlugTelemetry | undefined,
): PlugStateName => {
  if (!isDeviceConfigured(config)) return 'none';
  if (plug === undefined) return 'unknown';
  if (plug.online === false) return 'offline';
  if (plug.power === 'ON') return 'on';
  if (plug.power === 'OFF') return 'off';

  return 'unknown';
};

/** The catalogue key of each state's word. */
export const STATE_LABELS: Readonly<Record<PlugStateName, string>> = {
  none: 'power.state.none',
  on: 'power.state.on',
  off: 'power.state.off',
  offline: 'power.state.offline',
  unknown: 'power.state.unknown',
};

/** One badge look per state, so a state nobody styled shows as unstyled. */
export const STATE_BADGES: Readonly<Record<PlugStateName, BadgeVariant>> = {
  none: 'outline',
  on: 'success',
  off: 'secondary',
  offline: 'destructive',
  unknown: 'outline',
};

/** What the plug draws now, when it is on and reports a reading. */
export const wattsNow = (
  config: DeviceConfig | undefined,
  plug: PlugTelemetry | undefined,
): number | undefined =>
  plugStateOf(config, plug) === 'on' ? readingOf(plug?.power_watts) : undefined;
