import { defineModule } from '@prusa/connect-ui/slots';

import DashboardCard from './dashboard-card.svelte';
import { holdHost } from './host';
import PowerCard from './power-card.svelte';
import PowerPage from './power-page.svelte';
import PowerSection from './power-section.svelte';
import AutomationTab from './settings/automation-tab.svelte';
import ConnectionsTab from './settings/connections-tab.svelte';
import PlugsTab from './settings/plugs-tab.svelte';
import './styles.css';

export default defineModule((host) => {
  holdHost(host);

  // Every registration is held to `contributes` in module.yaml: a name the
  // manifest does not declare is refused at run time and the app drops this
  // module. Keep the two in step.
  host.register.page('power', PowerPage);
  host.register.deviceSection('power-plug', PowerSection);
  host.register.deviceCard('power-status', PowerCard);
  host.register.settings('plugs', PlugsTab);
  host.register.settings('connections', ConnectionsTab);
  host.register.settings('automation', AutomationTab);
  host.register.card('power', DashboardCard);
});
