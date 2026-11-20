<!--
  The Automation tab: when a plug cuts the power by itself. The first pane
  holds the rules for every printer, the second each printer's own choice.

  A settings pane takes no props, and the host draws it with no heading, so
  each pane names itself.
-->
<script lang="ts">
  import {
    Alert,
    AlertDescription,
    Button,
    Input,
    ListingSkeleton,
    SettingsPane,
    Switch,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
  } from '@prusa/connect-ui/primitives';
  import { onMount } from 'svelte';

  import { type DeviceConfig, isDeviceConfigured } from '../api';
  import { withAutoOff } from '../drivers';
  import FaultNote from '../fault-note.svelte';
  import RightsNote from '../rights-note.svelte';
  import { formatSpan } from '../format';
  import { heldHost } from '../host';
  import { typed, wholeOf } from '../input';
  import LinkButton from '../link-button.svelte';
  import { settingsTabHref } from '../links';
  import { powerFor } from '../power.svelte';
  import SettingRow from './setting-row.svelte';

  const DEFAULT_DELAY = 120;

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);
  const uid = $props.id();

  /** What is typed for the rules, undefined while a field shows what is saved. */
  let delayText = $state<string | undefined>(undefined);
  let confirmChoice = $state<boolean | undefined>(undefined);
  /** What is typed for one printer's own delay, by printer. */
  let ownDelays = $state<Record<string, string>>({});

  let saving = $state(false);
  let busyPrinter = $state('');
  let fault = $state.raw<unknown>(undefined);
  let justSaved = $state(false);

  onMount(() => {
    void power.loadSettings();
    void power.loadAssignments();

    return power.watch();
  });

  const saved = $derived(power.settings?.settings);
  const savedDelay = $derived(saved?.auto_off_cooldown_sec ?? DEFAULT_DELAY);
  const delay = $derived(delayText ?? String(savedDelay));
  const confirm = $derived(confirmChoice ?? saved?.confirm_turn_off ?? true);
  const dirty = $derived(
    saved !== undefined &&
      (wholeOf(delay, savedDelay) !== savedDelay ||
        confirm !== saved.confirm_turn_off),
  );

  const printers = $derived(
    [...host.fleet.devices].sort((a, b) =>
      (a.name || a.id).localeCompare(b.name || b.id),
    ),
  );

  const configFor = (deviceId: string): DeviceConfig | undefined =>
    power.entry(deviceId)?.config ?? power.assignments?.assignments[deviceId];

  const savedOwn = (config: DeviceConfig | undefined): string =>
    config?.cooldown_sec === undefined ? '' : String(config.cooldown_sec);

  const saveRules = async (event: SubmitEvent): Promise<void> => {
    event.preventDefault();
    saving = true;
    fault = undefined;
    justSaved = false;
    try {
      await power.saveSettings({
        auto_off_cooldown_sec: wholeOf(delay, savedDelay),
        confirm_turn_off: confirm,
      });
      delayText = undefined;
      confirmChoice = undefined;
      justSaved = true;
    } catch (thrown) {
      fault = thrown;
    } finally {
      saving = false;
    }
  };

  /** Runs one printer's change, and keeps what the process refused. */
  const change = async (
    deviceId: string,
    work: () => Promise<unknown>,
  ): Promise<boolean> => {
    busyPrinter = deviceId;
    fault = undefined;
    try {
      await work();

      return true;
    } catch (thrown) {
      fault = thrown;

      return false;
    } finally {
      busyPrinter = '';
    }
  };

  /** Takes a printer's own delay away, so it follows the general wait again. */
  const useGeneralWait = async (
    deviceId: string,
    config: DeviceConfig,
  ): Promise<void> => {
    await change(deviceId, () =>
      power.saveConfig(
        deviceId,
        withAutoOff(config, { cooldown_sec: undefined }),
      ),
    );
  };

  const saveOwnDelay = async (
    deviceId: string,
    config: DeviceConfig,
  ): Promise<void> => {
    const text = (ownDelays[deviceId] ?? '').trim();
    const done = await change(deviceId, () =>
      power.saveConfig(
        deviceId,
        withAutoOff(config, {
          cooldown_sec: text === '' ? undefined : wholeOf(text, savedDelay),
        }),
      ),
    );
    if (done) {
      ownDelays = Object.fromEntries(
        Object.entries(ownDelays).filter(([id]) => id !== deviceId),
      );
    }
  };
</script>

<div class="flex flex-col gap-6">
  <RightsNote />
  <form onsubmit={(event) => void saveRules(event)} data-part="rules">
    <SettingsPane
      title={t('power.automation.rules.title')}
      description={t('power.automation.rules.description')}
      edged
    >
      {#if saved === undefined}
        <FaultNote fault={power.settingsFault} />
        {#if power.settingsFault === undefined}
          <ListingSkeleton label={t('power.automation.reading')} rows={2} />
        {/if}
      {:else}
        <SettingRow
          id={`${uid}-delay`}
          label={t('power.automation.delay.label')}
          description={t('power.automation.delay.hint')}
        >
          {#snippet control()}
            <!-- The width is on a box of this module's own: an Input fills
                 the box it is in. -->
            <div class="w-24">
              <Input
                type="number"
                min={0}
                aria-labelledby={`${uid}-delay-label`}
                aria-describedby={`${uid}-delay-description`}
                value={delay}
                oninput={typed((value) => (delayText = value))}
              />
            </div>
            <span class="text-muted-foreground text-sm">
              {t('power.automation.seconds')}
            </span>
          {/snippet}
        </SettingRow>
        <SettingRow
          id={`${uid}-confirm`}
          label={t('power.automation.confirm.label')}
          description={t('power.automation.confirm.hint')}
        >
          {#snippet control()}
            <Switch
              aria-labelledby={`${uid}-confirm-label`}
              aria-describedby={`${uid}-confirm-description`}
              checked={confirm}
              onCheckedChange={(next: boolean) => (confirmChoice = next)}
            />
          {/snippet}
        </SettingRow>
        {#if justSaved}
          <Alert variant="success" data-part="saved">
            <AlertDescription>{t('power.connections.saved')}</AlertDescription>
          </Alert>
        {/if}
      {/if}

      {#snippet actions()}
        <Button type="submit" disabled={!dirty} pending={saving}>
          {t('power.connections.save')}
        </Button>
      {/snippet}
    </SettingsPane>
  </form>

  <SettingsPane
    title={t('power.automation.printers.title')}
    description={t('power.automation.printers.description', {
      delay: formatSpan(savedDelay),
    })}
    edged
  >
    <div class="flex flex-col gap-4">
      <FaultNote fault={fault ?? host.fleet.error} />
      {#if !host.fleet.loaded}
        <ListingSkeleton label={t('power.automation.reading')} rows={3} />
      {:else if printers.length === 0}
        <p class="text-muted-foreground text-sm" data-part="no-printers">
          {t('power.plugs.noPrinters')}
        </p>
      {:else}
        <Table data-part="printers">
          <TableHeader>
            <TableRow>
              <TableHead>{t('power.automation.printers.printer')}</TableHead>
              <TableHead>{t('power.automation.printers.autoOff')}</TableHead>
              <TableHead>{t('power.automation.printers.delay')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {#each printers as printer (printer.id)}
              {@const name = printer.name || printer.id}
              {@const config = configFor(printer.id)}
              {@const typedDelay = ownDelays[printer.id]}
              <TableRow data-printer={printer.id}>
                <TableCell class="font-medium">{name}</TableCell>
                {#if config !== undefined && isDeviceConfigured(config)}
                  <TableCell>
                    <Switch
                      aria-label={t('power.automation.printers.autoOffOf', {
                        printer: name,
                      })}
                      checked={config.auto_off_default === true}
                      disabled={busyPrinter === printer.id ||
                        !power.mayOperate(printer.id)}
                      onCheckedChange={(next: boolean) =>
                        void change(printer.id, () =>
                          power.setAutoOff(printer.id, next, true),
                        )}
                    />
                  </TableCell>
                  <TableCell>
                    <div class="flex items-center gap-2">
                      <div class="w-24">
                        <Input
                          size="sm"
                          type="number"
                          min={0}
                          aria-label={t('power.automation.printers.delayOf', {
                            printer: name,
                          })}
                          placeholder={String(savedDelay)}
                          value={typedDelay ?? savedOwn(config)}
                          oninput={typed(
                            (value) => (ownDelays[printer.id] = value),
                          )}
                        />
                      </div>
                      <span class="text-muted-foreground text-sm">
                        {t('power.automation.seconds')}
                      </span>
                      {#if typedDelay !== undefined && typedDelay.trim() !== savedOwn(config)}
                        <Button
                          size="sm"
                          pending={busyPrinter === printer.id}
                          onclick={() => void saveOwnDelay(printer.id, config)}
                        >
                          {t('power.connections.save')}
                        </Button>
                      {:else if savedOwn(config) === ''}
                        <span
                          class="text-muted-foreground text-xs"
                          data-part="general-wait"
                        >
                          {t('power.automation.printers.general')}
                        </span>
                      {:else}
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={busyPrinter === printer.id}
                          onclick={() =>
                            void useGeneralWait(printer.id, config)}
                        >
                          {t('power.automation.printers.useGeneral')}
                        </Button>
                      {/if}
                    </div>
                  </TableCell>
                {:else}
                  <TableCell colspan={2}>
                    <span class="text-muted-foreground">
                      {t('power.plugs.none')}
                    </span>
                    <LinkButton href={settingsTabHref('plugs')}>
                      {t('power.automation.printers.setUp')}
                    </LinkButton>
                  </TableCell>
                {/if}
              </TableRow>
            {/each}
          </TableBody>
        </Table>
      {/if}
    </div>
  </SettingsPane>
</div>
