import { defineModule } from '@prusa/connect-ui/slots';

import { holdHost } from './host';
import PowerSection from './power-section.svelte';
import PowerCard from './power-card.svelte';
import PowerSettings from './power-settings.svelte';
import './styles.css';

export default defineModule((host) => {
  holdHost(host);

  host.register.deviceSection('power-plug', PowerSection);
  host.register.deviceCard('power-status', PowerCard);
  host.register.settings('settings', PowerSettings);
});
