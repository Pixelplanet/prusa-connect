<!--
  The Connections tab: the two servers a plug may be reached through. A plug
  reached by its own address needs neither, and the tab says so.

  A settings pane takes no props, and the host draws it with no heading, so
  each pane names itself.
-->
<script lang="ts">
  import { ListingSkeleton } from '@prusa/connect-ui/primitives';
  import { onMount } from 'svelte';

  import FaultNote from '../fault-note.svelte';
  import RightsNote from '../rights-note.svelte';
  import { heldHost } from '../host';
  import { powerFor } from '../power.svelte';
  import HaPane from './ha-pane.svelte';
  import MqttPane from './mqtt-pane.svelte';

  /** How often the state of the connections is read while the tab is open. */
  const STATUS_MS = 5000;

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);

  onMount(() => {
    void power.loadSettings();
    const release = power.watch();
    // A broker connects a moment after it is saved, so the badge is read
    // again while a person looks at it.
    const timer = setInterval(() => {
      void power.loadSettings();
    }, STATUS_MS);

    return () => {
      clearInterval(timer);
      release();
    };
  });
</script>

<div class="flex flex-col gap-6">
  <RightsNote />
  {#if power.settings === undefined}
    <FaultNote fault={power.settingsFault} />
    {#if power.settingsFault === undefined}
      <ListingSkeleton label={t('power.connections.reading')} rows={3} />
    {/if}
  {:else}
    <MqttPane saved={power.settings} />
    <HaPane saved={power.settings} />
    <p class="text-muted-foreground text-sm" data-part="direct-note">
      {t('power.connections.direct', { count: power.used.http })}
    </p>
  {/if}
</div>
