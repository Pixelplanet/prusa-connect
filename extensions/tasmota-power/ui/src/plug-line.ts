// What a list says about one printer: which plug it has, and which picture
// stands for the printer.
import type { Translate } from '@prusa/connect-ui/i18n';
import {
  PRINTER_MODELS,
  type PrinterModel,
} from '@prusa/connect-ui/primitives';

import { type DeviceConfig, driverDisplayTarget, effectiveDriver } from './api';
import { kindOf } from './drivers';

/** The plug's address and its kind in one line, or '' for a printer with none. */
export const plugLineOf = (
  t: Translate,
  config: DeviceConfig | undefined,
): string => {
  const driver = effectiveDriver(config);
  if (driver === '') return '';

  return t('power.section.plug', {
    plug: driverDisplayTarget(config),
    type: t(kindOf(driver).name),
  });
};

const plain = (text: string): string =>
  text.toLowerCase().replace(/[^a-z0-9]/g, '');

const BY_SPELLING = new Map<string, PrinterModel>(
  PRINTER_MODELS.map((model) => [plain(model), model]),
);

/**
 * The artwork for a model as a printer reports it. A model the app has no
 * picture of is drawn as the unknown printer, with its own name beside it.
 */
export const artworkOf = (model: string): PrinterModel =>
  BY_SPELLING.get(plain(model)) ?? 'unknown';
