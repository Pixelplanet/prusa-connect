<!--
  Which relay of a plug switches the printer. A plug with one relay never
  shows this.
-->
<script lang="ts">
  import {
    Label,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
  } from '@prusa/connect-ui/primitives';

  import { heldHost } from '../host';

  let {
    value,
    count,
    onchange,
  }: {
    value: number;
    /** How many relays to offer. */
    count: number;
    onchange: (relay: number) => void;
  } = $props();

  const t = heldHost().t;
  const id = $props.id();

  const relays = $derived(
    Array.from({ length: Math.max(count, value, 1) }, (_, index) => index + 1),
  );
  const items = $derived(
    relays.map((relay) => ({
      value: String(relay),
      label: t('power.field.relay.option', { relay: String(relay) }),
    })),
  );
</script>

<div class="flex flex-col gap-1.5">
  <Label for={id}>{t('power.field.relay.label')}</Label>
  <Select
    value={String(value)}
    {items}
    onValueChange={(next: string) => {
      onchange(Number.parseInt(next, 10) || 1);
    }}
  >
    <SelectTrigger {id} class="w-full">
      <SelectValue />
    </SelectTrigger>
    <SelectContent aria-label={t('power.field.relay.label')}>
      {#each items as item (item.value)}
        <SelectItem value={item.value}>{item.label}</SelectItem>
      {/each}
    </SelectContent>
  </Select>
  <p class="text-muted-foreground text-xs">{t('power.field.relay.hint')}</p>
</div>
