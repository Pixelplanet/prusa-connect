<!--
  The Power Control page: every printer that has a plug, what the fleet draws
  now, and the buttons that switch each plug. A person looks at it as a table
  or as cards, like the printers page, and the choice is kept.
-->
<script lang="ts">
  import {
    Badge,
    Button,
    Card,
    CardContent,
    EmptyState,
    PageHeader,
    PrinterIcon,
    Skeleton,
    StatusCard,
    Switch,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
    viewModes,
    ViewModeToggle,
  } from '@prusa/connect-ui/primitives';
  import { printerHref } from '@prusa/connect-ui/shell';
  import { onMount } from 'svelte';

  import FaultNote from './fault-note.svelte';
  import { formatEnergy, formatSpan, formatWatts } from './format';
  import { heldHost } from './host';
  import LinkButton from './link-button.svelte';
  import { settingsTabHref } from './links';
  import PlugBadge from './plug-badge.svelte';
  import PlugCard from './plug-card.svelte';
  import { artworkOf, plugLineOf } from './plug-line';
  import { plugStateOf, wattsNow } from './plug-state';
  import PowerButtons from './power-buttons.svelte';
  import { powerFor } from './power.svelte';
  import { type PlugListRow, summaryOf } from './summary';

  /** The key the chosen view is kept under, in this module's own store. */
  const VIEW_KEY = 'power-page-view';

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);

  let fault = $state.raw<unknown>(undefined);
  /** The printer whose default is being changed. */
  let busyPrinter = $state('');
  let view = $state(host.store.get(VIEW_KEY) === 'cards' ? 'cards' : 'table');

  onMount(() => power.watch());

  const overview = $derived(power.overview);
  const views = $derived(viewModes(['table', 'cards']));

  /** Every printer the person may see that has a plug, by name. */
  const rows = $derived(
    host.fleet.devices
      .map((printer): PlugListRow => {
        const entry = power.entry(printer.id);

        return {
          id: printer.id,
          name: printer.name || printer.id,
          model: printer.model,
          entry,
          state: plugStateOf(entry?.config, entry?.plug),
          watts: wattsNow(entry?.config, entry?.plug),
        };
      })
      .filter((row) => row.state !== 'none')
      .sort((a, b) => a.name.localeCompare(b.name)),
  );
  const without = $derived(host.fleet.devices.length - rows.length);
  const totals = $derived(summaryOf(rows));

  const pickView = (next: string): void => {
    if (next !== 'table' && next !== 'cards') return;
    view = next;
    host.store.set(VIEW_KEY, next);
  };

  /** Sets whether a printer turns off after every print. */
  const setDefault = async (id: string, enabled: boolean): Promise<void> => {
    busyPrinter = id;
    fault = undefined;
    try {
      await power.setAutoOff(id, enabled, true);
    } catch (thrown) {
      fault = thrown;
    } finally {
      busyPrinter = '';
    }
  };
</script>

<div class="flex flex-col gap-6" data-part="power-page">
  <PageHeader title={t('power.page.title')}>
    {#snippet actions()}
      <div class="flex flex-wrap items-center justify-end gap-2">
        <ViewModeToggle value={view} onValueChange={pickView} options={views} />
        {#if power.mayConfigure}
          <LinkButton
            href={settingsTabHref('plugs')}
            variant="secondary"
            size="md"
          >
            {t('power.page.manage')}
          </LinkButton>
        {/if}
      </div>
    {/snippet}
  </PageHeader>

  {#if overview === undefined || !host.fleet.loaded}
    {#if power.overviewFault === undefined && host.fleet.error === undefined}
      <Skeleton
        class="h-24"
        role="status"
        aria-label={t('power.page.reading')}
      />
      <Skeleton class="h-48" />
    {:else}
      <EmptyState heading={t('power.page.failed')} headingLevel="h2">
        {#snippet action()}
          <FaultNote fault={power.overviewFault ?? host.fleet.error} />
          <Button onclick={() => void power.refresh()}>
            {t('power.page.retry')}
          </Button>
        {/snippet}
      </EmptyState>
    {/if}
  {:else if rows.length === 0}
    <EmptyState
      heading={t('power.page.empty')}
      description={t('power.page.emptyNote')}
      headingLevel="h2"
    >
      {#snippet action()}
        {#if power.mayConfigure}
          <LinkButton
            href={settingsTabHref('plugs')}
            variant="default"
            size="md"
          >
            {t('power.page.setUp')}
          </LinkButton>
        {/if}
      {/snippet}
    </EmptyState>
  {:else}
    <div class="grid grid-cols-2 gap-3 md:grid-cols-4" data-part="totals">
      <StatusCard
        edge
        tone={totals.watts > 0 ? 'active' : 'quiet'}
        title={t('power.page.total.now')}
        value={totals.metered > 0 ? formatWatts(totals.watts) : undefined}
        caption={t('power.page.total.metered', { count: totals.metered })}
      />
      <StatusCard
        edge
        tone={totals.on > 0 ? 'active' : 'quiet'}
        title={t('power.page.total.on')}
        value={t('power.page.total.onOf', {
          on: totals.on,
          all: rows.length,
        })}
      />
      <StatusCard
        edge
        tone="idle"
        title={t('power.page.total.energy')}
        value={formatEnergy(totals.energyWh)}
        caption={t('power.page.total.energyNote')}
      />
      <StatusCard
        edge
        tone={totals.silent > 0 ? 'fault' : 'quiet'}
        title={t('power.page.total.silent')}
        value={String(totals.silent)}
      />
    </div>

    <FaultNote fault={fault ?? power.overviewFault} />

    {#if view === 'cards'}
      <ul
        class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3"
        data-part="plug-cards"
      >
        {#each rows as row (row.id)}
          <li>
            <PlugCard
              {row}
              confirm={overview.confirm_turn_off}
              busy={busyPrinter === row.id}
              ondefault={(enabled: boolean) => void setDefault(row.id, enabled)}
              onfault={(thrown: unknown) => (fault = thrown)}
            />
          </li>
        {/each}
      </ul>
    {:else}
      <Card density="compact">
        <CardContent>
          <Table data-part="plugs">
            <TableHeader>
              <TableRow>
                <TableHead>{t('power.page.col.printer')}</TableHead>
                <TableHead>{t('power.page.col.state')}</TableHead>
                <TableHead class="text-end">
                  {t('power.page.col.now')}
                </TableHead>
                <TableHead class="text-end">
                  {t('power.page.col.session')}
                </TableHead>
                <TableHead>{t('power.page.col.plug')}</TableHead>
                <TableHead>{t('power.page.col.autoOff')}</TableHead>
                <TableHead class="text-end">
                  <span class="sr-only">{t('power.page.col.switch')}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {#each rows as row (row.id)}
                {@const session = row.entry?.session_usage}
                <TableRow data-printer={row.id}>
                  <TableCell>
                    <div class="flex items-center gap-3">
                      <PrinterIcon
                        model={artworkOf(row.model)}
                        reported={row.model}
                        class="size-11 shrink-0"
                      />
                      <a
                        class="font-medium hover:underline"
                        href={printerHref(row.id)}
                      >
                        {row.name}
                      </a>
                    </div>
                  </TableCell>
                  <TableCell>
                    <span class="inline-flex flex-wrap items-center gap-2">
                      <PlugBadge plugState={row.state} />
                      {#if row.entry?.config.auto_off_after_print}
                        <Badge variant="outline">
                          {t('power.page.autoOff')}
                        </Badge>
                      {/if}
                    </span>
                  </TableCell>
                  <TableCell class="text-end tabular-nums">
                    {row.watts === undefined
                      ? t('power.page.noReading')
                      : formatWatts(row.watts)}
                  </TableCell>
                  <TableCell class="text-end tabular-nums">
                    {#if session?.active}
                      {t('power.page.session', {
                        energy: formatEnergy(session.energy_wh),
                        span: formatSpan(session.duration_sec),
                      })}
                    {:else}
                      {t('power.page.noReading')}
                    {/if}
                  </TableCell>
                  <TableCell class="text-muted-foreground">
                    {plugLineOf(t, row.entry?.config)}
                  </TableCell>
                  <TableCell>
                    <Switch
                      aria-label={t('power.page.autoOffOf', {
                        printer: row.name,
                      })}
                      checked={row.entry?.config.auto_off_default === true}
                      disabled={busyPrinter === row.id ||
                        !power.mayOperate(row.id)}
                      onCheckedChange={(enabled: boolean) =>
                        void setDefault(row.id, enabled)}
                    />
                  </TableCell>
                  <TableCell>
                    <div class="flex justify-end">
                      <PowerButtons
                        deviceId={row.id}
                        printer={row.name}
                        plugState={row.state}
                        confirm={overview.confirm_turn_off}
                        onfault={(thrown: unknown) => (fault = thrown)}
                      />
                    </div>
                  </TableCell>
                </TableRow>
              {/each}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    {/if}

    {#if without > 0}
      <p class="text-muted-foreground text-sm" data-part="without">
        {t('power.page.without', { count: without })}
        {#if power.mayConfigure}
          <LinkButton href={settingsTabHref('plugs')}>
            {t('power.page.setUp')}
          </LinkButton>
        {/if}
      </p>
    {/if}
  {/if}
</div>
