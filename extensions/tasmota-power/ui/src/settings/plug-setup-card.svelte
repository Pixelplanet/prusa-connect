<!--
  One printer on the Plugs tab, as a card: its picture, the plug that switches
  it and what that plug is doing now, and the buttons that set the plug up,
  change it and remove it. The card keeps the buttons next to what they act
  on, where a row across the whole page put them far from it.
-->
<script lang="ts">
  import {
    Button,
    Card,
    CardContent,
    PrinterIcon,
  } from '@prusa/connect-ui/primitives';

  import {
    type DeviceConfig,
    driverDisplayTarget,
    effectiveDriver,
    isDeviceConfigured,
    type PlugTelemetry,
    relayIndexOf,
  } from '../api';
  import { kindOf, TRANSPORT_NAMES, transportOf } from '../drivers';
  import { formatWatts } from '../format';
  import { heldHost } from '../host';
  import PlugBadge from '../plug-badge.svelte';
  import { artworkOf } from '../plug-line';
  import { plugStateOf, wattsNow } from '../plug-state';

  let {
    id,
    name,
    model,
    config,
    plug,
    busy,
    onsetup,
    onremove,
  }: {
    id: string;
    name: string;
    /** The model as the printer reports it, for its picture. */
    model: string;
    config: DeviceConfig | undefined;
    plug: PlugTelemetry | undefined;
    /** A change of some plug is on its way. */
    busy: boolean;
    /** Opens the dialog that sets the plug up or changes it. */
    onsetup: () => void;
    onremove: () => void;
  } = $props();

  const t = heldHost().t;

  const plugState = $derived(plugStateOf(config, plug));
  const watts = $derived(wattsNow(config, plug));
  const driver = $derived(effectiveDriver(config));
  const has = $derived(driver !== '' && isDeviceConfigured(config));
  const relay = $derived(relayIndexOf(config));

  /** What the card says about the plug, one fact a line. */
  const facts = $derived.by((): { label: string; value: string }[] => {
    if (!has || driver === '') return [];

    return [
      { label: t('power.plugs.fact.type'), value: t(kindOf(driver).name) },
      {
        label: t('power.plugs.fact.address'),
        value: driverDisplayTarget(config),
      },
      // A plug with several relays says which one, on a line of its own so
      // a long address never hides it.
      ...(relay > 1
        ? [{ label: t('power.plugs.fact.relay'), value: String(relay) }]
        : []),
      {
        label: t('power.plugs.fact.via'),
        value: t(TRANSPORT_NAMES[transportOf(driver) ?? 'mqtt']),
      },
      {
        label: t('power.plugs.fact.now'),
        value:
          watts === undefined ? t('power.page.noReading') : formatWatts(watts),
      },
    ];
  });
</script>

<Card density="compact" class="h-full" data-printer={id}>
  <CardContent class="flex h-full flex-col gap-3">
    <div class="flex items-start gap-3">
      <PrinterIcon
        model={artworkOf(model)}
        reported={model}
        class="size-14 shrink-0"
      />
      <div class="flex min-w-0 flex-1 flex-col gap-1">
        <span class="truncate text-sm font-semibold">{name}</span>
        <span><PlugBadge {plugState} /></span>
      </div>
    </div>

    {#if has}
      <dl class="flex flex-col gap-1 text-sm">
        {#each facts as fact (fact.label)}
          <div class="flex items-start justify-between gap-3">
            <dt class="text-muted-foreground shrink-0 text-xs">
              {fact.label}
            </dt>
            <dd class="min-w-0 text-right break-all tabular-nums">
              {fact.value}
            </dd>
          </div>
        {/each}
      </dl>
    {:else}
      <p class="text-muted-foreground text-sm" data-part="no-plug-note">
        {t('power.plugs.noneNote')}
      </p>
    {/if}

    <div
      class="border-border mt-auto flex flex-wrap items-center justify-end gap-2 border-t pt-3"
    >
      {#if has}
        <Button
          size="sm"
          variant="ghost"
          aria-label={t('power.plugs.removeOf', { printer: name })}
          disabled={busy}
          onclick={onremove}
        >
          {t('power.plugs.remove')}
        </Button>
        <Button
          size="sm"
          variant="secondary"
          aria-label={t('power.plugs.changeOf', { printer: name })}
          disabled={busy}
          onclick={onsetup}
        >
          {t('power.plugs.change')}
        </Button>
      {:else}
        <Button
          size="sm"
          aria-label={t('power.plugs.setUpOf', { printer: name })}
          disabled={busy}
          onclick={onsetup}
        >
          {t('power.plugs.setUp')}
        </Button>
      {/if}
    </div>
  </CardContent>
</Card>
