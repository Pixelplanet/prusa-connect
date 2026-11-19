<script lang="ts">
  import { onMount } from 'svelte';

  import { createApi, type PowerStatus } from './api';
  import { heldHost } from './host';

  interface Props {
    deviceId: string;
  }

  let { deviceId }: Props = $props();

  const host = heldHost();
  const api = createApi(host);
  const t = host.t;

  let status = $state<PowerStatus | undefined>(undefined);

  async function refresh() {
    try {
      status = await api.getStatus(deviceId);
    } catch {
      // Ignore transient errors on the compact listing card.
    }
  }

  onMount(() => {
    void refresh();
    const timer = setInterval(() => {
      void refresh();
    }, 10000);
    return () => clearInterval(timer);
  });
</script>

{#if (status?.config.tasmota_topic || status?.config.http_host) && status.plug}
  <span class="text-xs text-muted-foreground">
    {#if status.plug.power === 'ON'}
      {#if status.plug.power_watts !== undefined}
        {t('tasmota-power.card.onWatts', {
          watts: Math.round(status.plug.power_watts),
        })}
      {:else}
        {t('tasmota-power.card.on')}
      {/if}
      {#if status.config.auto_off_after_print}
        · {t('tasmota-power.card.autoOff')}
      {/if}
    {:else if status.plug.power === 'OFF'}
      {t('tasmota-power.card.off')}
    {/if}
  </span>
{/if}
