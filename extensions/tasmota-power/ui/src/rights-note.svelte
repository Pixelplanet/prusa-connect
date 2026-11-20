<!--
  Said at the top of a settings tab to a person who may look at the set-up
  but not change it. Every change they try is refused anyway; this says why
  before they try. It draws nothing while the answer is not known yet.
-->
<script lang="ts">
  import {
    Alert,
    AlertDescription,
    AlertTitle,
  } from '@prusa/connect-ui/primitives';

  import { heldHost } from './host';
  import { powerFor } from './power.svelte';

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);
</script>

{#if !power.mayConfigure}
  <Alert variant="warning" data-part="no-configure">
    <AlertTitle>{t('power.rights.configureTitle')}</AlertTitle>
    <AlertDescription>{t('power.rights.configureNote')}</AlertDescription>
  </Alert>
{/if}
