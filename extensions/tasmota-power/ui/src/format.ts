// Readings as a person reads them, in the reader's own number format.
import {
  formatDuration,
  formatNumber,
  formatUnit,
} from '@prusa/connect-ui/i18n';

export const formatWatts = (watts: number): string =>
  formatUnit(Math.round(watts), 'W', { maximumFractionDigits: 0 });

export const formatVolts = (volts: number): string =>
  formatUnit(Math.round(volts), 'V', { maximumFractionDigits: 0 });

export const formatAmps = (amps: number): string =>
  formatUnit(amps, 'A', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });

/** Watt-hours below one kilowatt-hour, kilowatt-hours from there. */
export const formatEnergy = (wattHours: number): string =>
  wattHours >= 1000
    ? formatUnit(wattHours / 1000, 'kWh', {
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      })
    : formatUnit(wattHours, 'Wh', {
        minimumFractionDigits: 1,
        maximumFractionDigits: 1,
      });

/** A span of seconds. A span of nothing is zero seconds, never a blank. */
export const formatSpan = (seconds: number): string =>
  formatDuration(seconds) ||
  formatNumber(0, { style: 'unit', unit: 'second', unitDisplay: 'short' });
