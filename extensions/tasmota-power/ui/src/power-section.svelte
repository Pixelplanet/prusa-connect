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

  import { createApi, type PowerSample, type PowerStatus } from './api';
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
  let confirmingOff = $state(false);
  let confirmTimer: ReturnType<typeof setTimeout> | undefined;

  function clearConfirmOff() {
    confirmingOff = false;
    if (confirmTimer !== undefined) {
      clearTimeout(confirmTimer);
      confirmTimer = undefined;
    }
  }

  function requestTurnOff() {
    if (status?.confirm_turn_off === false) {
      void handlePower('off');
      return;
    }
    clearConfirmOff();
    confirmingOff = true;
    confirmTimer = setTimeout(() => {
      confirmingOff = false;
      confirmTimer = undefined;
    }, 6000);
  }

  function formatEnergy(wh: number): string {
    if (wh >= 1000) {
      return `${(wh / 1000).toFixed(2)} kWh`;
    }
    return `${wh.toFixed(1)} Wh`;
  }

  function formatDuration(totalSec: number): string {
    const s = Math.max(0, Math.floor(totalSec));
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const rem = s % 60;
    if (h > 0) return `${h}h ${m}m`;
    if (m > 0) return `${m}m ${rem}s`;
    return `${rem}s`;
  }

  interface ChartGeometry {
    linePoints: string;
    areaPoints: string;
    maxWatts: number;
    peakWatts: number;
    currentWatts: number;
    spanLabel: string;
    midSpanLabel: string;
    lastXPercent: number;
    lastYPercent: number;
  }

  function buildChart(samples: PowerSample[] | undefined): ChartGeometry | undefined {
    if (!samples || samples.length === 0) return undefined;
    const width = 1000;
    const height = 100;
    const padTop = 6;
    const padBottom = 6;
    const plotH = height - padTop - padBottom;

    let rawPeak = 0;
    for (const sample of samples) {
      if (sample.watts > rawPeak) rawPeak = sample.watts;
    }
    const maxWatts = Math.max(
      100,
      Math.ceil(Math.max(rawPeak * 1.15, 10) / 50) * 50,
    );
    const bottomY = padTop + plotH;

    const coords = samples.map((sample, idx) => {
      const ratio = samples.length === 1 ? 1 : idx / (samples.length - 1);
      const x = ratio * width;
      const clamped = Math.max(0, Math.min(maxWatts, sample.watts));
      const y = bottomY - (clamped / maxWatts) * plotH;
      return { x, y };
    });

    const first = coords[0];
    const last = coords[coords.length - 1];
    const polyPts =
      samples.length === 1
        ? [
            { x: 0, y: last.y },
            { x: width, y: last.y },
          ]
        : coords;

    const linePoints = polyPts
      .map((c) => `${c.x.toFixed(1)},${c.y.toFixed(1)}`)
      .join(' ');
    const startX = samples.length === 1 ? 0 : first.x;
    const areaPoints = `${startX.toFixed(1)},${bottomY.toFixed(1)} ${linePoints} ${last.x.toFixed(1)},${bottomY.toFixed(1)}`;

    const spanSec = Math.max(0, samples[samples.length - 1].ts - samples[0].ts);
    const spanLabel =
      spanSec >= 60 ? `-${Math.round(spanSec / 60)}m` : `-${spanSec}s`;
    const midSpanLabel =
      spanSec >= 120 ? `-${Math.round(spanSec / 120)}m` : '';

    return {
      linePoints,
      areaPoints,
      maxWatts,
      peakWatts: rawPeak,
      currentWatts: samples[samples.length - 1].watts,
      spanLabel,
      midSpanLabel,
      lastXPercent: (last.x / width) * 100,
      lastYPercent: (last.y / height) * 100,
    };
  }

  let chart = $derived(buildChart(status?.power_history));
  let printCardRecord = $derived(
    status?.current_print_usage ?? status?.print_history?.[0],
  );

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
    return () => {
      clearInterval(timer);
      clearConfirmOff();
    };
  });

  async function handlePower(action: 'on' | 'off') {
    clearConfirmOff();
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
            {#if status.plug?.online === false}
              <Badge variant="destructive">
                {t('tasmota-power.section.plugOffline')}
              </Badge>
            {:else if status.plug?.power === 'ON'}
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
              {#if (status.config.relay_index ?? 1) > 1 || (status.plug?.relay_count ?? 1) > 1}
                · {t('tasmota-power.section.relayBadge', {
                  relay: status.config.relay_index ?? 1,
                })}
              {/if}
            </span>

            {#if status.plug?.online === false}
              <span class="text-xs text-destructive">
                {t('tasmota-power.section.plugOfflineHint')}
              </span>
            {:else if status.plug?.power === 'ON' && status.plug.power_watts !== undefined}
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
              disabled={busy || status.plug?.online === false || status.plug?.power === 'ON'}
              onclick={() => void handlePower('on')}
            >
              {t('tasmota-power.section.turnOn')}
            </Button>
            {#if confirmingOff && status.plug?.online !== false && status.plug?.power !== 'OFF' && status.can_turn_off}
              <div class="flex items-center gap-1.5 rounded-sm border border-destructive/60 bg-destructive/10 px-2 py-0.5">
                <span class="text-xs font-medium text-destructive-foreground">
                  {t('tasmota-power.section.confirmOffPrompt')}
                </span>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={busy}
                  onclick={() => void handlePower('off')}
                >
                  {t('tasmota-power.section.confirmOffYes')}
                </Button>
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={busy}
                  onclick={clearConfirmOff}
                >
                  {t('tasmota-power.section.confirmOffCancel')}
                </Button>
              </div>
            {:else}
              <Button
                size="sm"
                variant="destructive"
                disabled={busy || status.plug?.online === false || !status.can_turn_off || status.plug?.power === 'OFF'}
                onclick={requestTurnOff}
              >
                {t('tasmota-power.section.turnOff')}
              </Button>
            {/if}
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

        <!-- Session & Print File Energy Summary -->
        <div class="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
          <!-- While Turned On Session -->
          <div class="flex flex-col justify-between gap-1.5 rounded-sm border border-border bg-background/50 p-2.5">
            <div class="flex items-center justify-between gap-2">
              <span class="text-[11px] font-medium text-muted-foreground">
                {status.session_usage?.active
                  ? t('tasmota-power.section.sessionActiveTitle')
                  : t('tasmota-power.section.sessionLastTitle')}
              </span>
              {#if status.session_usage}
                <Badge variant={status.session_usage.active ? 'success' : 'secondary'}>
                  {status.session_usage.active
                    ? t('tasmota-power.section.onBadge')
                    : t('tasmota-power.section.offBadge')}
                </Badge>
              {/if}
            </div>
            {#if status.session_usage}
              <div class="flex items-baseline justify-between gap-2">
                <span class="text-sm font-semibold text-foreground tabular-nums">
                  {formatEnergy(status.session_usage.energy_wh)}
                </span>
                <span class="text-xs text-muted-foreground tabular-nums">
                  {formatDuration(status.session_usage.duration_sec)}
                </span>
              </div>
              <span class="text-[11px] text-muted-foreground tabular-nums">
                {t('tasmota-power.section.avgPeak', {
                  avg: Math.round(status.session_usage.avg_watts),
                  peak: Math.round(status.session_usage.peak_watts),
                })}
              </span>
            {:else}
              <span class="text-xs text-muted-foreground">
                {t('tasmota-power.section.sessionNone')}
              </span>
            {/if}
          </div>

          <!-- Per-Print File Energy -->
          <div class="flex flex-col justify-between gap-1.5 rounded-sm border border-border bg-background/50 p-2.5 min-w-0">
            <div class="flex items-center justify-between gap-2">
              <span class="text-[11px] font-medium text-muted-foreground">
                {printCardRecord?.active
                  ? t('tasmota-power.section.printActiveTitle')
                  : t('tasmota-power.section.printLastTitle')}
              </span>
              {#if printCardRecord}
                <Badge variant={printCardRecord.active ? 'info' : 'secondary'}>
                  {printCardRecord.active
                    ? t('tasmota-power.section.printingBadge')
                    : printCardRecord.outcome || t('tasmota-power.section.offBadge')}
                </Badge>
              {/if}
            </div>
            {#if printCardRecord}
              <span
                class="truncate text-xs font-medium text-foreground"
                title={printCardRecord.file_name}
              >
                {printCardRecord.file_name}
              </span>
              <div class="flex items-baseline justify-between gap-2">
                <span class="text-sm font-semibold text-foreground tabular-nums">
                  {formatEnergy(printCardRecord.energy_wh)}
                </span>
                <span class="text-xs text-muted-foreground tabular-nums">
                  {formatDuration(printCardRecord.duration_sec)}
                </span>
              </div>
              <span class="text-[11px] text-muted-foreground tabular-nums">
                {t('tasmota-power.section.avgPeak', {
                  avg: Math.round(printCardRecord.avg_watts),
                  peak: Math.round(printCardRecord.peak_watts),
                })}
              </span>
            {:else}
              <span class="text-xs text-muted-foreground">
                {t('tasmota-power.section.noPrintYet')}
              </span>
            {/if}
          </div>
        </div>

        <!-- Live Power Graph -->
        <div class="flex flex-col gap-1.5 rounded-sm border border-border bg-background/50 p-2.5">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="text-[11px] font-medium text-muted-foreground">
              {t('tasmota-power.section.graphTitle')}
            </span>
            {#if chart}
              <span class="text-[11px] text-muted-foreground tabular-nums">
                {t('tasmota-power.section.graphLivePeak', {
                  watts: Math.round(chart.currentWatts),
                  peak: Math.round(chart.peakWatts),
                })}
              </span>
            {/if}
          </div>

          {#if chart}
            <div class="flex items-stretch gap-2">
              <div class="flex w-11 shrink-0 flex-col justify-between pb-4 pt-0.5 text-right text-[10px] text-muted-foreground tabular-nums select-none">
                <span>{chart.maxWatts} W</span>
                <span>{Math.round(chart.maxWatts / 2)} W</span>
                <span>0 W</span>
              </div>
              <div class="flex min-w-0 flex-1 flex-col gap-1">
                <div class="relative h-24 w-full">
                  <svg
                    viewBox="0 0 1000 100"
                    class="h-full w-full overflow-visible"
                    preserveAspectRatio="none"
                  >
                    <defs>
                      <linearGradient id="tasmota-pwr-grad-{deviceId}" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="0%" stop-color="var(--color-primary, #fa6831)" stop-opacity="0.32" />
                        <stop offset="100%" stop-color="var(--color-primary, #fa6831)" stop-opacity="0.02" />
                      </linearGradient>
                    </defs>

                    <line
                      x1="0"
                      y1="6"
                      x2="1000"
                      y2="6"
                      stroke="currentColor"
                      stroke-opacity="0.12"
                      stroke-dasharray="4 4"
                      vector-effect="non-scaling-stroke"
                    />
                    <line
                      x1="0"
                      y1="50"
                      x2="1000"
                      y2="50"
                      stroke="currentColor"
                      stroke-opacity="0.12"
                      stroke-dasharray="4 4"
                      vector-effect="non-scaling-stroke"
                    />
                    <line
                      x1="0"
                      y1="94"
                      x2="1000"
                      y2="94"
                      stroke="currentColor"
                      stroke-opacity="0.2"
                      vector-effect="non-scaling-stroke"
                    />

                    <polygon
                      points={chart.areaPoints}
                      fill="url(#tasmota-pwr-grad-{deviceId})"
                    />
                    <polyline
                      fill="none"
                      stroke="var(--color-primary, #fa6831)"
                      stroke-width="2"
                      vector-effect="non-scaling-stroke"
                      stroke-linejoin="round"
                      stroke-linecap="round"
                      points={chart.linePoints}
                    />
                  </svg>
                  <span
                    class="pointer-events-none absolute h-2.5 w-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full ring-2 ring-background"
                    style="left: {chart.lastXPercent}%; top: {chart.lastYPercent}%; background-color: var(--color-primary, #fa6831);"
                  ></span>
                </div>
                <div class="flex items-center justify-between text-[10px] text-muted-foreground tabular-nums select-none">
                  <span>{chart.spanLabel}</span>
                  {#if chart.midSpanLabel}
                    <span>{chart.midSpanLabel}</span>
                  {/if}
                  <span>Now</span>
                </div>
              </div>
            </div>
          {:else}
            <p class="py-4 text-center text-xs text-muted-foreground">
              {t('tasmota-power.section.graphEmpty')}
            </p>
          {/if}
        </div>

        <!-- Recent Print Files Energy History -->
        {#if status.print_history && status.print_history.length > 0}
          <div class="flex flex-col rounded-sm border border-border bg-background/50">
            <div class="px-2.5 py-1.5 border-b border-border">
              <span class="text-[11px] font-medium text-muted-foreground">
                {t('tasmota-power.section.recentPrintsTitle')}
              </span>
            </div>
            <div class="divide-y divide-border">
              {#each status.print_history.slice(0, 5) as item (item.started_at + item.file_name)}
                <div class="flex items-center justify-between gap-3 px-2.5 py-1.5 text-xs">
                  <span
                    class="truncate font-medium text-foreground min-w-0"
                    title={item.file_name}
                  >
                    {item.file_name}
                  </span>
                  <div class="flex items-center gap-3 shrink-0 text-muted-foreground tabular-nums">
                    <span>{formatDuration(item.duration_sec)}</span>
                    <span>{Math.round(item.avg_watts)} W</span>
                    <span class="font-semibold text-foreground">
                      {formatEnergy(item.energy_wh)}
                    </span>
                  </div>
                </div>
              {/each}
            </div>
          </div>
        {/if}
      {/if}

      {#if errorMsg}
        <p class="text-xs text-destructive">{errorMsg}</p>
      {/if}
    </CardContent>
  </Card>
{/if}
