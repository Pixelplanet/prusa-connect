<!--
  The Power Control section of a printer's page: what the plug is doing, the
  buttons that switch it, the choice to cut the power after a print, and what
  the printer drew.

  A printer with no plug, a plug whose connection is missing and a plug that
  does not answer are each said in their own words, with a link to where a
  person fixes it.
-->
<script lang="ts">
  import {
    Alert,
    AlertDescription,
    AlertTitle,
    Badge,
    Card,
    CardContent,
    DataRow,
    Label,
    RowList,
    Skeleton,
    StatusCard,
    Switch,
  } from '@prusa/connect-ui/primitives';
  import { onMount } from 'svelte';

  import {
    driverDisplayTarget,
    effectiveDriver,
    isDeviceConfigured,
    type PowerStatus,
    type PrintPowerRecord,
    readingOf,
    relayIndexOf,
    textOr,
  } from './api';
  import { kindOf, transportOf } from './drivers';
  import FaultNote from './fault-note.svelte';
  import {
    formatAmps,
    formatEnergy,
    formatSpan,
    formatVolts,
    formatWatts,
  } from './format';
  import { heldHost } from './host';
  import LinkButton from './link-button.svelte';
  import { settingsTabHref } from './links';
  import PlugBadge from './plug-badge.svelte';
  import { plugStateOf } from './plug-state';
  import PowerButtons from './power-buttons.svelte';
  import PowerChart from './power-chart.svelte';
  import { powerFor } from './power.svelte';

  interface Props {
    deviceId: string;
  }

  let { deviceId }: Props = $props();

  /** How often this printer's readings are read while its page is open. */
  const REFRESH_MS = 5000;
  /** How many finished prints the section lists. */
  const RECENT_PRINTS = 5;

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);
  const uid = $props.id();

  let status = $state.raw<PowerStatus | undefined>(undefined);
  let readFault = $state.raw<unknown>(undefined);
  let fault = $state.raw<unknown>(undefined);
  let busy = $state(false);

  const printer = $derived(textOr(host.fleet.device(deviceId)?.name, deviceId));
  const config = $derived(status?.config);
  const plug = $derived(status?.plug);
  const driver = $derived(effectiveDriver(config));
  const transport = $derived(transportOf(driver));
  const plugState = $derived(plugStateOf(config, plug));

  /** Why the plug cannot be used at all, or undefined when it can. */
  const blocked = $derived.by(
    (): 'mqttUnset' | 'mqttDown' | 'haUnset' | undefined => {
      if (status === undefined) return undefined;
      if (transport === 'mqtt' && !status.mqtt_configured) return 'mqttUnset';
      if (transport === 'mqtt' && !status.mqtt_connected) return 'mqttDown';
      if (transport === 'ha' && status.ha_configured === false)
        return 'haUnset';

      return undefined;
    },
  );

  /** Which plug this is, in one line: its name, its kind and its relay. */
  const plugLine = $derived.by((): string => {
    if (driver === '') return '';
    const address = driverDisplayTarget(config);
    const name =
      plug?.friendly_name !== undefined &&
      plug.friendly_name !== '' &&
      plug.friendly_name !== address
        ? t('power.setup.plug.named', {
            name: plug.friendly_name,
            topic: address,
          })
        : address;
    const params = { plug: name, type: t(kindOf(driver).name) };
    const relay = relayIndexOf(config);

    return relay > 1 || (plug?.relay_count ?? 1) > 1
      ? t('power.section.plugRelay', { ...params, relay: String(relay) })
      : t('power.section.plug', params);
  });

  const reading = $derived.by((): string => {
    const watts = readingOf(plug?.power_watts);
    const volts = readingOf(plug?.voltage);
    const amps = readingOf(plug?.current_amps);
    if (plugState !== 'on' || watts === undefined) return '';
    if (volts === undefined || amps === undefined) return formatWatts(watts);

    return t('power.section.reading', {
      watts: formatWatts(watts),
      volts: formatVolts(volts),
      amps: formatAmps(amps),
    });
  });

  /** Why the printer stayed on after its last print, in words. */
  const keptText = $derived.by((): string => {
    if (status?.auto_off_kept === 'cancelled') {
      return t('power.section.autoOffKept.cancelled');
    }
    if (status?.auto_off_kept === 'failed') {
      return t('power.section.autoOffKept.failed');
    }

    return '';
  });

  const samples = $derived(status?.power_history ?? []);
  const peak = $derived(
    samples.reduce((most, sample) => Math.max(most, sample.watts), 0),
  );
  const latest = $derived(samples[samples.length - 1]);
  const session = $derived(status?.session_usage);
  const print = $derived(
    status?.current_print_usage ?? status?.print_history?.[0],
  );
  const recent = $derived(
    (status?.print_history ?? []).slice(0, RECENT_PRINTS),
  );

  const usageOf = (record: {
    duration_sec: number;
    avg_watts: number;
    peak_watts: number;
  }): string =>
    t('power.section.usage', {
      span: formatSpan(record.duration_sec),
      average: formatWatts(record.avg_watts),
      peak: formatWatts(record.peak_watts),
    });

  const printKey = (record: PrintPowerRecord): string =>
    `${record.started_at} ${record.file_name}`;

  const refresh = async (): Promise<void> => {
    try {
      status = await power.api.getStatus(deviceId);
      readFault = undefined;
    } catch (thrown) {
      readFault = thrown;
    }
  };

  onMount(() => {
    void refresh();
    const timer = setInterval(() => {
      void refresh();
    }, REFRESH_MS);
    // The overview says whether the person may switch this plug.
    const release = power.watch();

    return () => {
      clearInterval(timer);
      release();
    };
  });

  const setAutoOff = async (enabled: boolean): Promise<void> => {
    busy = true;
    fault = undefined;
    try {
      status = await power.setAutoOff(deviceId, enabled);
    } catch (thrown) {
      fault = thrown;
    } finally {
      busy = false;
    }
  };
</script>

<Card density="compact" data-part="power-section">
  <CardContent class="flex flex-col gap-4">
    {#if status === undefined}
      {#if readFault === undefined}
        <Skeleton
          class="h-16"
          role="status"
          aria-label={t('power.section.loading')}
        />
      {:else}
        <FaultNote fault={readFault} />
      {/if}
    {:else if !isDeviceConfigured(config)}
      <div
        class="flex flex-wrap items-center justify-between gap-3"
        data-part="no-plug"
      >
        <p class="text-muted-foreground text-sm">{t('power.section.noPlug')}</p>
        <LinkButton
          href={settingsTabHref('plugs')}
          variant="secondary"
          size="sm"
        >
          {t('power.section.setUp')}
        </LinkButton>
      </div>
    {:else if blocked !== undefined}
      <Alert
        variant={blocked === 'mqttDown' ? 'destructive' : 'warning'}
        data-part="blocked"
        data-reason={blocked}
      >
        <AlertTitle>{t(`power.section.blocked.${blocked}`)}</AlertTitle>
        <AlertDescription>
          {#if blocked === 'mqttDown' && status.mqtt_error}
            {status.mqtt_error}
          {/if}
          <LinkButton href={settingsTabHref('connections')}>
            {t('power.plugs.openConnections')}
          </LinkButton>
        </AlertDescription>
      </Alert>
    {:else}
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <PlugBadge {plugState} />
          <span class="text-muted-foreground truncate text-sm">{plugLine}</span>
          {#if reading !== ''}
            <span class="text-sm tabular-nums" data-part="reading">
              {reading}
            </span>
          {/if}
        </div>
        <PowerButtons
          {deviceId}
          {printer}
          {plugState}
          confirm={status.confirm_turn_off !== false}
          locked={!status.can_turn_off}
          onswitched={(next: PowerStatus) => (status = next)}
          onfault={(thrown: unknown) => (fault = thrown)}
        />
      </div>

      {#if plugState === 'offline'}
        <Alert variant="destructive" data-part="offline">
          <AlertTitle>{t('power.section.offline')}</AlertTitle>
          {#if plug?.last_error}
            <AlertDescription>{plug.last_error}</AlertDescription>
          {/if}
        </Alert>
      {/if}

      <FaultNote {fault} />

      <div
        class="border-border flex flex-wrap items-center justify-between gap-2 border-t pt-3"
      >
        <div class="flex items-center gap-2">
          <Switch
            id={`${uid}-auto-off`}
            checked={status.config.auto_off_after_print}
            disabled={busy || !power.mayOperate(deviceId)}
            onCheckedChange={(next: boolean) => void setAutoOff(next)}
          />
          <Label for={`${uid}-auto-off`} variant="option">
            {t('power.section.autoOff')}
          </Label>
          <span
            class="text-muted-foreground text-xs"
            data-part="auto-off-default"
          >
            {status.config.auto_off_default
              ? t('power.section.autoOffDefaultOn')
              : t('power.section.autoOffDefaultOff')}
          </span>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          {#if status.auto_off_pending}
            {#if status.printer_moving}
              <Badge variant="warning">
                {t('power.section.autoOffMoving')}
              </Badge>
            {:else}
              <Badge variant="info">
                {t('power.section.autoOffIn', {
                  span: formatSpan(status.auto_off_remaining_sec),
                })}
              </Badge>
            {/if}
          {/if}
          {#if keptText !== ''}
            <Badge variant="outline" data-part="auto-off-kept">{keptText}</Badge
            >
          {/if}
          {#if !status.can_turn_off && status.lock_reason}
            <span class="text-muted-foreground text-xs" data-part="lock">
              {status.lock_reason}
            </span>
          {/if}
        </div>
      </div>

      {#if status.auto_off_error}
        <Alert variant="destructive" data-part="auto-off-failed">
          <AlertTitle>{t('power.section.autoOffFailed')}</AlertTitle>
          <AlertDescription>{status.auto_off_error}</AlertDescription>
        </Alert>
      {/if}

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <StatusCard
          edge
          headingLevel="h4"
          tone={session?.active ? 'active' : 'quiet'}
          title={session?.active
            ? t('power.section.session.now')
            : t('power.section.session.last')}
          value={session ? formatEnergy(session.energy_wh) : undefined}
          caption={session ? usageOf(session) : t('power.section.session.none')}
        />
        <StatusCard
          edge
          headingLevel="h4"
          tone={print?.active ? 'active' : 'quiet'}
          title={print?.active
            ? t('power.section.print.now')
            : t('power.section.print.last')}
          value={print ? formatEnergy(print.energy_wh) : undefined}
          caption={print
            ? t('power.section.print.of', {
                file: print.file_name,
                span: formatSpan(print.duration_sec),
              })
            : t('power.section.print.none')}
        />
      </div>

      <div class="flex flex-col gap-2" data-part="graph">
        <div class="flex flex-wrap items-baseline justify-between gap-2">
          <h4 class="text-muted-foreground text-xs font-medium">
            {t('power.section.graph.title')}
          </h4>
          {#if latest !== undefined}
            <span class="text-muted-foreground text-xs tabular-nums">
              {t('power.section.graph.nowPeak', {
                now: formatWatts(latest.watts),
                peak: formatWatts(peak),
              })}
            </span>
          {/if}
        </div>
        {#if samples.length > 1}
          <PowerChart {samples} class="h-40" />
          <p class="text-muted-foreground text-xs">
            {t('power.section.graph.step', {
              span: formatSpan(status.poll_interval_sec ?? 5),
            })}
          </p>
        {:else}
          <p class="text-muted-foreground py-3 text-center text-xs">
            {t('power.section.graph.empty')}
          </p>
        {/if}
      </div>

      {#if recent.length > 0}
        <div class="flex flex-col gap-2" data-part="recent-prints">
          <h4 class="text-muted-foreground text-xs font-medium">
            {t('power.section.recent')}
          </h4>
          <RowList>
            {#each recent as record (printKey(record))}
              <DataRow
                mono
                primary={record.file_name}
                secondaryValues={[
                  formatEnergy(record.energy_wh),
                  usageOf(record),
                ]}
              />
            {/each}
          </RowList>
        </div>
      {/if}
    {/if}
  </CardContent>
</Card>
