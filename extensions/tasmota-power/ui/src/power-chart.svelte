<!--
  What a plug drew over the last half hour, on the library's own chart so it
  reads the same as every other chart in the app, in both themes.
-->
<script lang="ts">
  import { LineChart } from '@prusa/connect-ui/primitives';

  import type { PowerSample } from './api';
  import { heldHost } from './host';

  let {
    samples,
    class: className,
  }: {
    samples: readonly PowerSample[];
    class?: string;
  } = $props();

  const t = heldHost().t;

  /** The smallest step on the axis, so an idle printer is not drawn as noise. */
  const MIN_STEP_WATTS = 10;

  const series = $derived([
    {
      id: 'watts',
      label: t('power.section.graph.series'),
      points: samples.map((sample) => ({
        at: sample.ts * 1000,
        value: sample.watts,
      })),
    },
  ]);
</script>

<LineChart
  {series}
  unit="W"
  minStep={MIN_STEP_WATTS}
  label={t('power.section.graph.label')}
  class={className}
/>
