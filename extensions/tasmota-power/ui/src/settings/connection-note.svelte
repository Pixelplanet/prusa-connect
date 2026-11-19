<!--
  Whether the connection a plug needs is there, said where a person picks the
  plug. A plug reached by its own address needs none, and this draws nothing.
-->
<script lang="ts">
  import { Alert, AlertDescription } from '@prusa/connect-ui/primitives';

  import type { Transport } from '../drivers';
  import { heldHost } from '../host';
  import LinkButton from '../link-button.svelte';
  import { settingsTabHref } from '../links';
  import { powerFor } from '../power.svelte';

  let { transport }: { transport: Transport } = $props();

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);

  const settings = $derived(power.settings);
  const mqttUp = $derived(
    settings?.mqtt_connected ?? power.overview?.mqtt_connected,
  );
  const haReady = $derived(
    settings?.ha_configured ?? power.overview?.ha_configured,
  );
</script>

{#if transport === 'mqtt' && mqttUp === true}
  <p class="text-muted-foreground text-xs" data-part="connection-ready">
    {settings?.mqtt_broker
      ? t('power.setup.connection.mqttReadyAt', {
          broker: settings.mqtt_broker,
        })
      : t('power.setup.connection.mqttReady')}
  </p>
{:else if transport === 'mqtt' && mqttUp === false}
  <Alert variant="warning" data-part="connection-missing">
    <AlertDescription>
      {t('power.setup.connection.mqttMissing')}
      <LinkButton href={settingsTabHref('connections')}>
        {t('power.setup.connection.open')}
      </LinkButton>
    </AlertDescription>
  </Alert>
{:else if transport === 'ha' && haReady === true}
  <p class="text-muted-foreground text-xs" data-part="connection-ready">
    {t('power.setup.connection.haReady')}
  </p>
{:else if transport === 'ha' && haReady === false}
  <Alert variant="warning" data-part="connection-missing">
    <AlertDescription>
      {t('power.setup.connection.haMissing')}
      <LinkButton href={settingsTabHref('connections')}>
        {t('power.setup.connection.open')}
      </LinkButton>
    </AlertDescription>
  </Alert>
{/if}
