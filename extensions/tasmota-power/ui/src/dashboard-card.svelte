<!--
  A dashboard card of what the printers draw now and which plugs are on. It
  counts the printers a person picked in its settings, and every printer when
  they picked none.

  With room for it, the card draws each printer's power over the last minutes
  as one line, on the library's chart and over the same span the core's
  temperature card offers, so the two cards line up when one sits above the
  other. Without room, or with the graph switched off, it lists the printers.

  The card polls nothing of its own. It reads the one overview the store keeps
  for every screen, which carries the samples while a graph is shown, and
  links to the Power page for the rest.
-->
<script lang="ts">
  import {
    type ChartSeries,
    LineChart,
    Skeleton,
    TilePanel,
  } from '@prusa/connect-ui/primitives';
  import { type CardProps, moduleIcon } from '@prusa/connect-ui/slots';
  import { onMount } from 'svelte';

  import FaultNote from './fault-note.svelte';
  import { formatWatts } from './format';
  import { heldHost } from './host';
  import PlugBadge from './plug-badge.svelte';
  import { plugStateOf, wattsNow } from './plug-state';
  import { powerFor } from './power.svelte';
  import { summaryOf } from './summary';

  let { config, title, size, editing }: CardProps = $props();

  /** The grid rows the heading and the figure take before the list starts. */
  const HEAD_ROWS = 2;
  /** How many printers fit in one grid row of the list. */
  const PRINTERS_PER_ROW = 2;
  /** The fewest grid rows that leave the graph room to be read. */
  const GRAPH_ROWS = 4;
  /** The spans the card offers, in minutes, as the temperature card does. */
  const SPANS = [5, 15, 30, 60] as const;
  const DEFAULT_SPAN = 15;
  /** The smallest step on the axis, so an idle printer is not drawn as noise. */
  const MIN_STEP_WATTS = 10;
  /** How often the time axis moves on, the same as the overview is read. */
  const TICK_MS = 5000;

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);
  const PageIcon = moduleIcon('chart-line');

  // The graph is on unless the person switched it off in the card's settings.
  const graphOn = $derived(config.graph !== false);
  const minutes = $derived.by((): number => {
    const picked = Number(config.minutes);
    return SPANS.find((span) => span === picked) ?? DEFAULT_SPAN;
  });
  const showGraph = $derived(graphOn && size.h >= GRAPH_ROWS);

  let now = $state(Date.now());

  onMount(() => {
    const timer = setInterval(() => {
      now = Date.now();
    }, TICK_MS);

    return () => {
      clearInterval(timer);
    };
  });

  // The overview carries the samples only while some card draws them, for
  // the longest span drawn. The card asks again when it starts or stops
  // drawing, or draws another span.
  $effect(() => power.watch({ graphMinutes: showGraph ? minutes : 0 }));

  const picked = $derived(
    Array.isArray(config.printers)
      ? config.printers.filter((id): id is string => typeof id === 'string')
      : [],
  );

  const rows = $derived(
    host.fleet.devices
      .filter((printer) => picked.length === 0 || picked.includes(printer.id))
      .map((printer) => {
        const entry = power.entry(printer.id);

        return {
          id: printer.id,
          name: printer.name || printer.id,
          entry,
          state: plugStateOf(entry?.config, entry?.plug),
          watts: wattsNow(entry?.config, entry?.plug),
        };
      })
      .filter((row) => row.state !== 'none')
      .sort((a, b) => a.name.localeCompare(b.name)),
  );
  const totals = $derived(summaryOf(rows));
  const room = $derived(Math.max(0, (size.h - HEAD_ROWS) * PRINTERS_PER_ROW));
  const listed = $derived(rows.slice(0, room));
  const more = $derived(rows.length - listed.length);

  const since = $derived(now - minutes * 60_000);
  const series = $derived.by((): ChartSeries[] =>
    rows.map((row) => ({
      id: row.id,
      label: row.name,
      points: (row.entry?.power_history ?? [])
        .map((sample) => ({ at: sample.ts * 1000, value: sample.watts }))
        .filter((point) => point.at >= since),
    })),
  );
  const quiet = $derived(series.every((line) => line.points.length === 0));
</script>

<TilePanel
  {title}
  fallback={t('power.dashboard.heading')}
  subline={picked.length === 0
    ? t('power.dashboard.everyPrinter')
    : t('power.dashboard.printers', { count: picked.length })}
>
  {#snippet trailing()}
    {#if !editing}
      <a
        href={host.pageHref('power')}
        class="text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:ring-ring shrink-0 rounded-sm p-1 focus-visible:ring-2 focus-visible:outline-none"
        aria-label={t('power.dashboard.open')}
        data-part="dashboard-open"
      >
        <PageIcon aria-hidden="true" class="size-4" />
      </a>
    {/if}
  {/snippet}

  {#if power.overview === undefined}
    {#if power.overviewFault === undefined}
      <Skeleton
        class="h-16"
        role="status"
        aria-label={t('power.page.reading')}
      />
    {:else}
      <FaultNote fault={power.overviewFault} />
    {/if}
  {:else if rows.length === 0}
    <p class="text-muted-foreground text-sm" data-part="empty">
      {t('power.dashboard.empty')}
    </p>
  {:else}
    <div class="flex min-h-0 flex-1 flex-col gap-3">
      <div class="flex flex-wrap items-baseline justify-between gap-2">
        <span class="text-xl font-semibold tabular-nums" data-part="total">
          {totals.metered > 0
            ? formatWatts(totals.watts)
            : t('power.page.noReading')}
        </span>
        <span class="text-muted-foreground text-xs">
          {t('power.dashboard.on', { on: totals.on, all: rows.length })}
        </span>
      </div>
      {#if showGraph}
        {#if !power.carriesGraph(minutes)}
          <Skeleton class="min-h-0 w-full flex-1" />
        {:else if quiet}
          <div class="flex flex-col gap-1 text-center" data-part="graph-quiet">
            <p class="text-sm">{t('power.dashboard.quiet')}</p>
            <p class="text-muted-foreground text-xs">
              {t('power.dashboard.quietNote')}
            </p>
          </div>
        {:else}
          <LineChart
            {series}
            unit="W"
            minStep={MIN_STEP_WATTS}
            xDomain={[since, now]}
            label={t('power.dashboard.graphLabel', {
              printers: rows.map((row) => row.name).join(', '),
              minutes,
            })}
            class="min-h-0 flex-1"
          />
        {/if}
      {:else}
        {#if listed.length > 0}
          <ul class="flex min-h-0 flex-col gap-1.5" data-part="printers">
            {#each listed as row (row.id)}
              <li class="flex items-center justify-between gap-2 text-sm">
                <span class="min-w-0 truncate">{row.name}</span>
                <span class="flex shrink-0 items-center gap-2">
                  {#if row.watts !== undefined}
                    <span class="text-muted-foreground text-xs tabular-nums">
                      {formatWatts(row.watts)}
                    </span>
                  {/if}
                  <PlugBadge plugState={row.state} />
                </span>
              </li>
            {/each}
          </ul>
        {/if}
        {#if more > 0}
          <p class="text-muted-foreground text-xs" data-part="more">
            {t('power.dashboard.more', { count: more })}
          </p>
        {/if}
      {/if}
    </div>
  {/if}
</TilePanel>
