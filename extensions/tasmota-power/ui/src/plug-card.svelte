<!--
  One printer on the Power Control page, as a card: its picture, what its plug
  is doing, what it draws, whether it turns off after every print, and the
  buttons that switch it.
-->
<script lang="ts">
  import {
    Badge,
    Card,
    CardContent,
    Label,
    PrinterIcon,
    Switch,
  } from '@prusa/connect-ui/primitives';
  import { printerHref } from '@prusa/connect-ui/shell';

  import { formatEnergy, formatSpan, formatWatts } from './format';
  import { heldHost } from './host';
  import PlugBadge from './plug-badge.svelte';
  import { artworkOf, plugLineOf } from './plug-line';
  import PowerButtons from './power-buttons.svelte';
  import type { PlugListRow } from './summary';

  let {
    row,
    confirm,
    busy,
    ondefault,
    onfault,
  }: {
    row: PlugListRow;
    /** Ask before turning off. */
    confirm: boolean;
    /** A change of this printer's default is on its way. */
    busy: boolean;
    ondefault: (enabled: boolean) => void;
    onfault: (thrown: unknown) => void;
  } = $props();

  const t = heldHost().t;
  const uid = $props.id();

  const config = $derived(row.entry?.config);
  const session = $derived(row.entry?.session_usage);
</script>

<Card density="compact" class="h-full" data-printer={row.id}>
  <CardContent class="flex h-full flex-col gap-3">
    <div class="flex items-start gap-3">
      <PrinterIcon
        model={artworkOf(row.model)}
        reported={row.model}
        class="size-14 shrink-0"
      />
      <div class="flex min-w-0 flex-1 flex-col gap-1">
        <a
          class="truncate text-sm font-semibold hover:underline"
          href={printerHref(row.id)}
        >
          {row.name}
        </a>
        <span class="text-muted-foreground truncate text-xs">
          {plugLineOf(t, config)}
        </span>
      </div>
      <div class="flex shrink-0 flex-col items-end gap-1">
        <PlugBadge plugState={row.state} />
        {#if config?.auto_off_after_print}
          <Badge variant="outline">{t('power.page.autoOff')}</Badge>
        {/if}
      </div>
    </div>

    <dl class="grid grid-cols-2 gap-3">
      <div class="flex min-w-0 flex-col">
        <dt class="text-muted-foreground text-xs">
          {t('power.page.col.now')}
        </dt>
        <dd class="text-sm font-semibold tabular-nums">
          {row.watts === undefined
            ? t('power.page.noReading')
            : formatWatts(row.watts)}
        </dd>
      </div>
      <div class="flex min-w-0 flex-col">
        <dt class="text-muted-foreground text-xs">
          {t('power.page.col.session')}
        </dt>
        <dd class="text-sm font-semibold tabular-nums">
          {session?.active
            ? t('power.page.session', {
                energy: formatEnergy(session.energy_wh),
                span: formatSpan(session.duration_sec),
              })
            : t('power.page.noReading')}
        </dd>
      </div>
    </dl>

    <div
      class="border-border mt-auto flex flex-wrap items-center justify-between gap-3 border-t pt-3"
    >
      <div class="flex items-center gap-2">
        <Switch
          id={`${uid}-default`}
          checked={config?.auto_off_default === true}
          disabled={busy}
          onCheckedChange={ondefault}
        />
        <Label for={`${uid}-default`} variant="option">
          {t('power.page.col.autoOff')}
        </Label>
      </div>
      <PowerButtons
        deviceId={row.id}
        printer={row.name}
        plugState={row.state}
        {confirm}
        {onfault}
      />
    </div>
  </CardContent>
</Card>
