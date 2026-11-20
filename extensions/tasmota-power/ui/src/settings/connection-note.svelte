<!--
  Whether the connection a plug needs is there, said where a person picks the
  plug. A plug reached by its own address needs none, and this draws nothing.
-->
<script lang="ts">
  import { Alert, AlertDescription } from '@prusa/connect-ui/primitives';

  import { textOr } from '../api';
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
  // The address a plug on the network reaches the built-in broker at.
  const builtinHost = $derived(
    textOr(settings?.settings.mqtt_host, window.location.hostname),
  );
  const haReady = $derived(
    settings?.ha_configured ?? power.overview?.ha_configured,
  );
</script>

{#if transport === 'mqtt' && mqttUp === true && settings?.mqtt_builtin_running}
  <Alert variant="success" data-part="connection-builtin">
    <AlertDescription>
      {t('power.setup.connection.builtinReady', {
        host: builtinHost,
        port: String(settings.mqtt_builtin_port ?? 1883),
        user: settings.settings.mqtt_username,
      })}
    </AlertDescription>
  </Alert>
{:else if transport === 'mqtt' && mqttUp === true}
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
      {#if settings?.mqtt_error}
        {settings.mqtt_error}
      {/if}
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
