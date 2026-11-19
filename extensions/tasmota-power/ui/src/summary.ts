// The figures a list of plugs adds up to. The Power page and the dashboard
// card show the same four, so they are worked out in one place.
import type { OverviewEntry } from './api';
import type { PlugStateName } from './plug-state';

export interface PlugRow {
  readonly state: PlugStateName;
  /** What the plug draws now, when it is on and reports a reading. */
  readonly watts: number | undefined;
  readonly entry: OverviewEntry | undefined;
}

export interface PlugTotals {
  /** What every plug that reports a reading draws now, added up. */
  readonly watts: number;
  /** How many plugs report a reading. A total of none is not a zero. */
  readonly metered: number;
  /** How many plugs are on. */
  readonly on: number;
  /** How many plugs do not answer. */
  readonly silent: number;
  /** The energy since each plug that is on was switched on, added up. */
  readonly energyWh: number;
}

export const summaryOf = (rows: readonly PlugRow[]): PlugTotals => {
  let watts = 0;
  let metered = 0;
  let on = 0;
  let silent = 0;
  let energyWh = 0;

  for (const row of rows) {
    if (row.state === 'on') on += 1;
    if (row.state === 'offline') silent += 1;
    if (row.watts !== undefined) {
      watts += row.watts;
      metered += 1;
    }
    const session = row.entry?.session_usage;
    if (session?.active) energyWh += session.energy_wh;
  }

  return { watts, metered, on, silent, energyWh };
};
