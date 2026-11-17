<script lang="ts">
  import { onMount } from 'svelte';
  import { Badge, Button } from '@prusa/connect-ui/primitives';

  import {
    createApi,
    type BrokerStatus,
    type BrokerTestResult,
  } from './api';
  import { heldHost } from './host';

  const host = heldHost();
  const api = createApi(host);
  const t = host.t;

  let broker = $state<BrokerStatus | undefined>(undefined);
  let testing = $state(false);
  let testResult = $state<BrokerTestResult | undefined>(undefined);
  let loadError = $state('');

  async function refresh() {
    try {
      broker = await api.getBrokerStatus();
      loadError = '';
    } catch (err) {
      loadError = err instanceof Error ? err.message : String(err);
    }
  }

  onMount(() => {
    void refresh();
  });

  async function handleTest() {
    testing = true;
    testResult = undefined;
    try {
      testResult = await api.testBroker();
      await refresh();
    } catch (err) {
      testResult = {
        ok: false,
        error: err instanceof Error ? err.message : String(err),
      };
    } finally {
      testing = false;
    }
  }
</script>

<div class="rounded-lg border p-4 space-y-3">
  <div class="flex items-center justify-between gap-2">
    <h3 class="text-sm font-semibold">{t('tasmota-power.settings.title')}</h3>
    {#if broker}
      {#if broker.connected}
        <Badge variant="default">{t('tasmota-power.settings.connected')}</Badge>
      {:else}
        <Badge variant="destructive">
          {t('tasmota-power.settings.disconnected')}
        </Badge>
      {/if}
    {/if}
  </div>

  {#if broker && !broker.configured}
    <p class="text-xs text-muted-foreground">
      {t('tasmota-power.settings.notConfigured')}
    </p>
  {:else if broker?.broker}
    <p class="text-xs text-muted-foreground">
      {t('tasmota-power.settings.broker', { broker: broker.broker })}
    </p>
  {/if}

  {#if broker?.error}
    <p class="text-xs text-destructive">{broker.error}</p>
  {/if}

  <div class="flex items-center gap-2">
    <Button
      size="sm"
      variant="outline"
      disabled={testing || !broker?.configured}
      onclick={() => void handleTest()}
    >
      {testing
        ? t('tasmota-power.settings.testing')
        : t('tasmota-power.settings.testButton')}
    </Button>
  </div>

  {#if testResult}
    {#if testResult.ok}
      <p class="text-xs text-green-600">{t('tasmota-power.settings.testOk')}</p>
    {:else}
      <p class="text-xs text-destructive">
        {t('tasmota-power.settings.testFail', {
          error: testResult.error ?? 'unknown error',
        })}
      </p>
    {/if}
  {/if}

  {#if loadError}
    <p class="text-xs text-destructive">{loadError}</p>
  {/if}
</div>
