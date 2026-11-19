<!--
  One line on a printer's card in the listing: what its plug is doing. A
  printer with no plug gets no line. A plug that does not answer is said, so a
  blank line never stands for one.

  Every card reads the one overview the store keeps, so a page of printers
  asks the process once.
-->
<script lang="ts">
  import { onMount } from 'svelte';

  import { formatWatts } from './format';
  import { heldHost } from './host';
  import { plugStateOf, STATE_LABELS, wattsNow } from './plug-state';
  import { powerFor } from './power.svelte';

  interface Props {
    deviceId: string;
  }

  let { deviceId }: Props = $props();

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);

  onMount(() => power.watch());

  const entry = $derived(power.entry(deviceId));
  const plugState = $derived(plugStateOf(entry?.config, entry?.plug));
  const watts = $derived(wattsNow(entry?.config, entry?.plug));

  const line = $derived.by((): string => {
    const plug =
      watts === undefined
        ? t('power.card.plug', { state: t(STATE_LABELS[plugState]) })
        : t('power.card.plugWatts', {
            state: t(STATE_LABELS[plugState]),
            watts: formatWatts(watts),
          });

    return entry?.config.auto_off_after_print
      ? t('power.card.autoOff', { plug })
      : plug;
  });
</script>

{#if plugState !== 'none'}
  <span class="text-muted-foreground text-sm" data-plug-state={plugState}>
    {line}
  </span>
{/if}
