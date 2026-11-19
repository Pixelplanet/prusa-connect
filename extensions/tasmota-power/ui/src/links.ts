// Addresses of the app's own screens this module links to. The host builds the
// address of a module's page; it has no helper for a tab of the module's page
// under Settings, Extensions, so that one is written here.
import { OWN_MODULE } from './own-module';

export type SettingsTab = 'plugs' | 'connections' | 'automation';

const EXTENSIONS = '/settings/extensions';

const ownPage = (): string =>
  OWN_MODULE === ''
    ? EXTENSIONS
    : `${EXTENSIONS}/${encodeURIComponent(OWN_MODULE)}`;

/** One of this module's tabs on its page under Settings, Extensions. */
export const settingsTabHref = (tab: SettingsTab): string =>
  OWN_MODULE === ''
    ? EXTENSIONS
    : `${ownPage()}/${encodeURIComponent(`${OWN_MODULE}.${tab}`)}`;

/** The host's own Ports tab of this module, where a port is approved. */
export const portsTabHref = (): string =>
  OWN_MODULE === '' ? EXTENSIONS : `${ownPage()}/ports`;
