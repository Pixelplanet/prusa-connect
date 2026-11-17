<script lang="ts">
  import { onMount } from 'svelte';
  import { Badge, Button } from '@prusa/connect-ui/primitives';

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
  let topicInput = $state('');
  let busy = $state(false);
  let errorMsg = $state('');

  async function refresh() {
    try {
      const next = await api.getStatus(deviceId);
      status = next;
      if (!topicInput && next.config.tasmota_topic) {
        topicInput = next.config.tasmota_topic;
      }
      errorMsg = '';
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    }
  }

  onMount(() => {
    void refresh();
    const timer = setInterval(() => {
      void refresh();
    }, 5000);
    return () => clearInterval(timer);
  });

  async function handlePower(action: 'on' | 'off') {
    busy = true;
    errorMsg = '';
    try {
      status = await api.sendPower(deviceId, action);
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      busy = false;
    }
  }

  async function handleAutoOff(enabled: boolean) {
    busy = true;
    errorMsg = '';
    try {
      status = await api.setAutoOff(deviceId, enabled);
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      busy = false;
    }
  }

  async function handleSaveTopic() {
    busy = true;
    errorMsg = '';
    try {
      status = await api.saveConfig(deviceId, {
        tasmota_topic: topicInput.trim(),
        auto_off_after_print: status?.config.auto_off_after_print ?? false,
        cooldown_sec: status?.config.cooldown_sec,
      });
      topicInput = status.config.tasmota_topic;
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      busy = false;
    }
  }
</script>

<div class="rounded-lg border p-4 space-y-3">
  <div class="flex items-center justify-between gap-2">
    <h3 class="text-sm font-semibold">{t('tasmota-power.section.title')}</h3>
    {#if status?.plug}
      <div class="flex items-center gap-2">
        {#if status.plug.power === 'ON'}
          <Badge variant="default">{t('tasmota-power.section.plugOn')}</Badge>
        {:else if status.plug.power === 'OFF'}
          <Badge variant="secondary">{t('tasmota-power.section.plugOff')}</Badge>
        {:else}
          <Badge variant="outline">{t('tasmota-power.section.plugUnknown')}</Badge>
        {/if}
        {#if status.plug.power_watts !== undefined && status.plug.power === 'ON'}
          <span class="text-xs text-muted-foreground">
            {t('tasmota-power.section.watts', {
              watts: Math.round(status.plug.power_watts),
            })}
          </span>
        {/if}
      </div>
    {/if}
  </div>

  {#if status && !status.mqtt_configured}
    <p class="text-xs text-muted-foreground">
      {t('tasmota-power.section.brokerUnconfigured')}
    </p>
  {:else if status && !status.mqtt_connected}
    <p class="text-xs text-destructive">
      {#if status.mqtt_error}
        {t('tasmota-power.section.brokerOfflineReason', {
          reason: status.mqtt_error,
        })}
      {:else}
        {t('tasmota-power.section.brokerOffline')}
      {/if}
    </p>
  {/if}

  {#if status?.config.tasmota_topic}
    <div class="flex flex-wrap items-center gap-2">
      <Button
        size="sm"
        variant="default"
        disabled={busy || status.plug?.power === 'ON'}
        onclick={() => void handlePower('on')}
      >
        {t('tasmota-power.section.turnOn')}
      </Button>
      <Button
        size="sm"
        variant="destructive"
        disabled={busy || !status.can_turn_off || status.plug?.power === 'OFF'}
        onclick={() => void handlePower('off')}
      >
        {t('tasmota-power.section.turnOff')}
      </Button>
    </div>

    {#if !status.can_turn_off && status.lock_reason}
      <p class="text-xs text-muted-foreground">{status.lock_reason}</p>
    {/if}

    <label class="flex items-center gap-2 text-xs cursor-pointer select-none">
      <input
        type="checkbox"
        checked={status.config.auto_off_after_print}
        disabled={busy}
        onchange={(e) =>
          void handleAutoOff((e.currentTarget as HTMLInputElement).checked)}
      />
      <span>{t('tasmota-power.section.autoOffLabel')}</span>
      {#if status.auto_off_pending}
        <Badge variant="secondary">
          {t('tasmota-power.section.autoOffPending', {
            seconds: status.auto_off_remaining_sec,
          })}
        </Badge>
      {/if}
    </label>
  {/if}

  <div class="flex items-center gap-2 pt-1">
    <label class="text-xs text-muted-foreground shrink-0" for="tasmota-topic">
      {t('tasmota-power.section.topicLabel')}
    </label>
    <input
      id="tasmota-topic"
      type="text"
      class="flex-1 rounded border bg-background px-2 py-1 text-xs"
      placeholder={t('tasmota-power.section.topicPlaceholder')}
      bind:value={topicInput}
    />
    <Button
      size="sm"
      variant="outline"
      disabled={busy}
      onclick={() => void handleSaveTopic()}
    >
      {t('tasmota-power.section.saveTopic')}
    </Button>
  </div>

  {#if errorMsg}
    <p class="text-xs text-destructive">{errorMsg}</p>
  {/if}
</div>
