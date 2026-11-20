<!--
  The Plugs tab: every printer as a card, with its picture, the plug that
  switches it and what that plug is doing now. A plug is set up, changed and
  removed from here.

  A settings pane takes no props, and the host draws it with no heading, so
  the pane names itself.
-->
<script lang="ts">
  import {
    Alert,
    AlertDescription,
    ConfirmDialog,
    ListingSkeleton,
    SettingsPane,
  } from '@prusa/connect-ui/primitives';
  import { onMount } from 'svelte';

  import type { DeviceConfig } from '../api';
  import { clearedConfig } from '../drivers';
  import FaultNote from '../fault-note.svelte';
  import RightsNote from '../rights-note.svelte';
  import { heldHost } from '../host';
  import LinkButton from '../link-button.svelte';
  import { settingsTabHref } from '../links';
  import { powerFor } from '../power.svelte';
  import { PANE_BODY } from './pane-body';
  import PlugDialog from './plug-dialog.svelte';
  import PlugSetupCard from './plug-setup-card.svelte';

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
  <div class={PANE_BODY}>
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

    <RightsNote />
    <FaultNote fault={fault ?? power.assignmentsFault ?? host.fleet.error} />

    {#if reading}
      <ListingSkeleton label={t('power.plugs.reading')} rows={3} />
    {:else if printers.length === 0}
      <p class="text-muted-foreground text-sm" data-part="no-printers">
        {t('power.plugs.noPrinters')}
      </p>
    {:else}
      <!-- The grid of the Power page's cards, so one printer does not
           stretch across a wide page. -->
      <ul
        class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3"
        data-part="plugs"
      >
        {#each printers as printer (printer.id)}
          {@const name = printer.name || printer.id}
          <li>
            <PlugSetupCard
              id={printer.id}
              {name}
              model={printer.model}
              config={configFor(printer.id)}
              plug={power.entry(printer.id)?.plug}
              {busy}
              onsetup={() => (setting = { id: printer.id, name })}
              onremove={() => (removing = { id: printer.id, name })}
            />
          </li>
        {/each}
      </ul>
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
