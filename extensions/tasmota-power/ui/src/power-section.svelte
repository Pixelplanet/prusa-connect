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
  let busy = $state(false);
  let errorMsg = $state('');

  async function refresh() {
    try {
      status = await api.getStatus(deviceId);
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
</script>

{#if status}
  {#if !status.config.tasmota_topic}
    <div class="rounded-lg border px-4 py-3 flex items-center justify-between gap-3">
      <p class="text-xs text-muted-foreground">
        {t('tasmota-power.section.notAssigned')}
      </p>
      <a
        href="/settings/extensions"
        class="text-xs font-medium text-primary hover:underline shrink-0"
      >
        {t('tasmota-power.section.configureLink')}
      </a>
    </div>
  {:else}
    <div class="rounded-lg border p-4 space-y-3">
      <div class="flex items-center justify-between gap-2">
        <div class="flex items-center gap-2">
          <h3 class="text-sm font-semibold">
            {t('tasmota-power.section.title')}
          </h3>
          <span class="text-xs text-muted-foreground">
            ({status.config.tasmota_topic})
          </span>
        </div>

        {#if status.plug}
          <div class="flex items-center gap-2">
            {#if status.plug.power === 'ON' && status.plug.power_watts !== undefined}
              <span class="text-xs text-muted-foreground tabular-nums">
                {#if status.plug.voltage !== undefined && status.plug.current_amps !== undefined}
                  {t('tasmota-power.section.telemetry', {
                    watts: Math.round(status.plug.power_watts),
                    voltage: Math.round(status.plug.voltage),
                    amps: status.plug.current_amps.toFixed(2),
                  })}
                {:else}
                  {t('tasmota-power.section.watts', {
                    watts: Math.round(status.plug.power_watts),
                  })}
                {/if}
              </span>
            {/if}

            {#if status.plug.power === 'ON'}
              <Badge variant="default">
                {t('tasmota-power.section.plugOn')}
              </Badge>
            {:else if status.plug.power === 'OFF'}
              <Badge variant="secondary">
                {t('tasmota-power.section.plugOff')}
              </Badge>
            {:else}
              <Badge variant="outline">
                {t('tasmota-power.section.plugUnknown')}
              </Badge>
            {/if}
          </div>
        {/if}
      </div>

      {#if !status.mqtt_configured}
        <p class="text-xs text-muted-foreground">
          {t('tasmota-power.section.brokerUnconfigured')}
        </p>
      {:else if !status.mqtt_connected}
        <p class="text-xs text-destructive">
          {#if status.mqtt_error}
            {t('tasmota-power.section.brokerOfflineReason', {
              reason: status.mqtt_error,
            })}
          {:else}
            {t('tasmota-power.section.brokerOffline')}
          {/if}
        </p>
      {:else}
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex items-center gap-2">
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
        </div>

        {#if !status.can_turn_off && status.lock_reason}
          <p class="text-xs text-muted-foreground">{status.lock_reason}</p>
        {/if}
      {/if}

      {#if errorMsg}
        <p class="text-xs text-destructive">{errorMsg}</p>
      {/if}
    </div>
  {/if}
{/if}
