<!--
  What a request was refused with, drawn by its code. It draws nothing while
  there is no fault.
-->
<script lang="ts">
  import { describeFault } from '@prusa/connect-ui/i18n';
  import { FaultAlert } from '@prusa/connect-ui/primitives';

  let { fault }: { fault: unknown } = $props();
</script>

{#if fault !== undefined}
  <FaultAlert fault={describeFault(fault)} />
{/if}
