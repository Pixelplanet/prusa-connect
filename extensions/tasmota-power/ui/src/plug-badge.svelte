<!--
  The state of one plug in a word. Every screen draws it with this, so a plug
  reads the same in the settings, on the printer and on the Power page.
-->
<script lang="ts">
  import { Badge } from '@prusa/connect-ui/primitives';

  import { heldHost } from './host';
  import { type PlugStateName, STATE_BADGES, STATE_LABELS } from './plug-state';

  let { plugState }: { plugState: PlugStateName } = $props();

  const t = heldHost().t;
</script>

<Badge variant={STATE_BADGES[plugState]} data-plug-state={plugState}>
  {t(STATE_LABELS[plugState])}
</Badge>
