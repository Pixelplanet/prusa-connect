<!--
  The Plugs tab: every printer with the plug that switches it and what that
  plug is doing now. A plug is set up, changed and removed from here.

  A settings pane takes no props, and the host draws it with no heading, so
  the pane names itself.
-->
<script lang="ts">
  import {
    Alert,
    AlertDescription,
    Button,
    ConfirmDialog,
    DataRow,
    ListingSkeleton,
    RowList,
    SettingsPane,
  } from '@prusa/connect-ui/primitives';
  import { onMount } from 'svelte';

  import {
    type DeviceConfig,
    driverDisplayTarget,
    effectiveDriver,
    isDeviceConfigured,
    relayIndexOf,
  } from '../api';
  import {
    clearedConfig,
    kindOf,
    TRANSPORT_NAMES,
    transportOf,
  } from '../drivers';
  import FaultNote from '../fault-note.svelte';
  import { formatWatts } from '../format';
  import { heldHost } from '../host';
  import LinkButton from '../link-button.svelte';
  import { settingsTabHref } from '../links';
  import PlugBadge from '../plug-badge.svelte';
  import { plugStateOf, wattsNow } from '../plug-state';
  import { powerFor } from '../power.svelte';
  import PlugDialog from './plug-dialog.svelte';

  interface Target {
    id: string;
    name: string;
  }

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);

  /** The printer whose plug the dialog is open for. */
  let setting = $state.raw<Target | undefined>(undefined);
  /** The printer whose plug waits on a confirmation to be removed. */
  let removing = $state.raw<Target | undefined>(undefined);
  let busy = $state(false);
  let fault = $state.raw<unknown>(undefined);

  onMount(() => {
    void power.loadSettings();
    void power.loadAssignments();

    return power.watch();
  });

  const printers = $derived(
    [...host.fleet.devices].sort((a, b) =>
      (a.name || a.id).localeCompare(b.name || b.id),
    ),
  );

  const configFor = (deviceId: string): DeviceConfig | undefined =>
    power.entry(deviceId)?.config ?? power.assignments?.assignments[deviceId];

  // How many plugs need each connection, so a missing one is named once.
  const needed = $derived(power.used);
  const mqttMissing = $derived(
    needed.mqtt > 0 && power.overview?.mqtt_connected === false,
  );
  const haMissing = $derived(
    needed.ha > 0 && power.overview?.ha_configured === false,
  );

  const reading = $derived(
    !host.fleet.loaded ||
      (power.assignments === undefined && power.assignmentsFault === undefined),
  );

  /** One line saying which plug a printer has and how it is reached. */
  const summaryOf = (config: DeviceConfig | undefined): string => {
    const driver = effectiveDriver(config);
    if (driver === '' || !isDeviceConfigured(config)) {
      return t('power.plugs.none');
    }
    const params = {
      type: t(kindOf(driver).name),
      address: driverDisplayTarget(config),
      via: t(TRANSPORT_NAMES[transportOf(driver) ?? 'mqtt']),
    };
    const relay = relayIndexOf(config);

    return relay > 1
      ? t('power.plugs.summaryRelay', { ...params, relay: String(relay) })
      : t('power.plugs.summary', params);
  };

  const remove = async (): Promise<void> => {
    const target = removing;
    removing = undefined;
    if (target === undefined) return;
    busy = true;
    fault = undefined;
    try {
      await power.saveConfig(target.id, clearedConfig(configFor(target.id)));
    } catch (thrown) {
      fault = thrown;
    } finally {
      busy = false;
    }
  };
</script>

<SettingsPane
  title={t('power.plugs.title')}
  description={t('power.plugs.description')}
  edged
>
  <div class="flex flex-col gap-4">
    {#if mqttMissing}
      <Alert variant="warning" data-part="mqtt-missing">
        <AlertDescription>
          {t('power.plugs.mqttMissing', { count: needed.mqtt })}
          <LinkButton href={settingsTabHref('connections')}>
            {t('power.plugs.openConnections')}
          </LinkButton>
        </AlertDescription>
      </Alert>
    {/if}
    {#if haMissing}
      <Alert variant="warning" data-part="ha-missing">
        <AlertDescription>
          {t('power.plugs.haMissing', { count: needed.ha })}
          <LinkButton href={settingsTabHref('connections')}>
            {t('power.plugs.openConnections')}
          </LinkButton>
        </AlertDescription>
      </Alert>
    {/if}

    <FaultNote fault={fault ?? power.assignmentsFault ?? host.fleet.error} />

    {#if reading}
      <ListingSkeleton label={t('power.plugs.reading')} rows={3} />
    {:else if printers.length === 0}
      <p class="text-muted-foreground text-sm" data-part="no-printers">
        {t('power.plugs.noPrinters')}
      </p>
    {:else}
      <RowList data-part="plugs">
        {#each printers as printer (printer.id)}
          {@const name = printer.name || printer.id}
          {@const config = configFor(printer.id)}
          {@const plug = power.entry(printer.id)?.plug}
          {@const plugState = plugStateOf(config, plug)}
          {@const watts = wattsNow(config, plug)}

          {#snippet stateCell()}
            <span class="inline-flex items-center gap-2">
              <PlugBadge {plugState} />
              {#if watts !== undefined}
                <span class="tabular-nums">{formatWatts(watts)}</span>
              {/if}
            </span>
          {/snippet}

          {#snippet rowActions()}
            <div class="flex items-center gap-2">
              {#if plugState === 'none'}
                <Button
                  size="sm"
                  aria-label={t('power.plugs.setUpOf', { printer: name })}
                  disabled={busy}
                  onclick={() => (setting = { id: printer.id, name })}
                >
                  {t('power.plugs.setUp')}
                </Button>
              {:else}
                <Button
                  size="sm"
                  variant="secondary"
                  aria-label={t('power.plugs.changeOf', { printer: name })}
                  disabled={busy}
                  onclick={() => (setting = { id: printer.id, name })}
                >
                  {t('power.plugs.change')}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label={t('power.plugs.removeOf', { printer: name })}
                  disabled={busy}
                  onclick={() => (removing = { id: printer.id, name })}
                >
                  {t('power.plugs.remove')}
                </Button>
              {/if}
            </div>
          {/snippet}

          <DataRow
            primary={name}
            secondaryValues={[
              stateCell,
              plugState !== 'none' && summaryOf(config),
            ]}
            actions={rowActions}
            data-printer={printer.id}
          />
        {/each}
      </RowList>
    {/if}
  </div>
</SettingsPane>

{#if setting !== undefined}
  <PlugDialog
    device={setting}
    onclose={() => {
      setting = undefined;
    }}
  />
{/if}

<ConfirmDialog
  open={removing !== undefined}
  onOpenChange={(open: boolean) => {
    if (!open) removing = undefined;
  }}
  title={t('power.plugs.removeAsk', { printer: removing?.name ?? '' })}
  description={t('power.plugs.removeNote')}
  confirmLabel={t('power.plugs.removeConfirm')}
  onConfirm={() => void remove()}
/>
