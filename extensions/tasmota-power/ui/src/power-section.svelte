<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Badge,
    Button,
    Card,
    CardContent,
    Label,
    Switch,
  } from '@prusa/connect-ui/primitives';

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
  <Card density="compact">
    <CardContent class="flex flex-col gap-3">
      {#if !status.config.tasmota_topic}
        <div class="flex items-center justify-between gap-3">
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
      {:else if !status.mqtt_configured}
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
            {#if status.plug?.power === 'ON'}
              <Badge variant="success">
                {t('tasmota-power.section.plugOn')}
              </Badge>
            {:else if status.plug?.power === 'OFF'}
              <Badge variant="secondary">
                {t('tasmota-power.section.plugOff')}
              </Badge>
            {:else}
              <Badge variant="outline">
                {t('tasmota-power.section.plugUnknown')}
              </Badge>
            {/if}

            <span class="text-xs text-muted-foreground">
              {status.plug?.friendly_name
                ? `${status.plug.friendly_name} (${status.config.tasmota_topic})`
                : status.config.tasmota_topic}
            </span>

            {#if status.plug?.power === 'ON' && status.plug.power_watts !== undefined}
              <span class="text-xs text-muted-foreground tabular-nums">
                ·
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
          </div>

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
        </div>

        <div class="flex flex-wrap items-center justify-between gap-2 pt-1 border-t border-border">
          <div class="flex items-center gap-2">
            <Switch
              id="tasmota-auto-off-{deviceId}"
              checked={status.config.auto_off_after_print}
              disabled={busy}
              onCheckedChange={(v: boolean) => void handleAutoOff(v)}
            />
            <Label for="tasmota-auto-off-{deviceId}" variant="option">
              {t('tasmota-power.section.autoOffLabel')}
            </Label>
          </div>

          <div class="flex items-center gap-2">
            {#if status.auto_off_pending}
              {#if status.printer_moving}
                <Badge variant="warning">
                  {t('tasmota-power.section.autoOffMoving')}
                </Badge>
              {:else}
                <Badge variant="secondary">
                  {t('tasmota-power.section.autoOffPending', {
                    seconds: status.auto_off_remaining_sec,
                  })}
                </Badge>
              {/if}
            {/if}
            {#if !status.can_turn_off && status.lock_reason}
              <span class="text-xs text-muted-foreground">
                {status.lock_reason}
              </span>
            {/if}
          </div>
        </div>
      {/if}

      {#if errorMsg}
        <p class="text-xs text-destructive">{errorMsg}</p>
      {/if}
    </CardContent>
  </Card>
{/if}
