<!--
  One setting: its name and what it does at the start, its control at the end.

  It stands in for the library's SettingsRow. That one lays itself out with
  `flex-col` and a container variant that turns it into a row. Inside a
  module's slot this bundle's own `flex-col` is nearer than the host's
  variant, so the library's row stays a column here (measured in a browser).
  This one names only utilities of its own.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    id,
    label,
    description,
    control,
  }: {
    /** The label is `<id>-label` and the description `<id>-description`. */
    id: string;
    label: string;
    description: string;
    control: Snippet;
  } = $props();
</script>

<div
  class="flex flex-wrap items-center justify-between gap-3 py-3"
  data-part="setting-row"
>
  <div class="flex min-w-0 flex-1 flex-col gap-0.5">
    <span id={`${id}-label`} class="text-sm font-medium">{label}</span>
    <span id={`${id}-description`} class="text-muted-foreground text-xs">
      {description}
    </span>
  </div>
  <div class="flex shrink-0 items-center gap-2">
    {@render control()}
  </div>
</div>
