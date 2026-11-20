<!--
  The buttons that switch one printer's plug. A plug that is on offers Turn
  off, one that is off offers Turn on, and one nothing is known about offers
  both. Turning off cuts the printer's mains power, so it asks first unless
  the person switched the question off in the Automation tab.
-->
<script lang="ts">
  import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
    Button,
  } from '@prusa/connect-ui/primitives';

  import type { PowerStatus } from './api';
  import { heldHost } from './host';
  import { OWN_MODULE } from './own-module';
  import type { PlugStateName } from './plug-state';
  import { powerFor } from './power.svelte';

  let {
    deviceId,
    printer,
    plugState,
    confirm,
    locked = false,
    onswitched,
    onfault,
  }: {
    deviceId: string;
    /** The printer's name, for the question and the buttons' names. */
    printer: string;
    plugState: PlugStateName;
    /** Ask before turning off. */
    confirm: boolean;
    /** The printer is busy, so turning off is refused. */
    locked?: boolean;
    onswitched?: (status: PowerStatus) => void;
    /** What the process refused, or undefined when a new press starts. */
    onfault: (thrown: unknown) => void;
  } = $props();

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);

  let busy = $state(false);
  let asking = $state(false);

  const unreachable = $derived(plugState === 'offline' || plugState === 'none');

  const send = async (action: 'on' | 'off'): Promise<void> => {
    asking = false;
    busy = true;
    onfault(undefined);
    try {
      // Sent before the callback is looked at: an optional call with no
      // callback does not evaluate its argument, and the plug must switch
      // whether or not a screen wants the answer.
      const status = await power.sendPower(deviceId, action);
      onswitched?.(status);
    } catch (thrown) {
      onfault(thrown);
    } finally {
      busy = false;
    }
  };

  const turnOff = (): void => {
    if (confirm) asking = true;
    else void send('off');
  };
</script>

{#if power.mayOperate(deviceId)}
  <div class="flex items-center gap-2" data-part="power-buttons">
    {#if plugState !== 'on'}
      <Button
        size="sm"
        aria-label={t('power.switch.onOf', { printer })}
        disabled={busy || unreachable}
        onclick={() => void send('on')}
      >
        {t('power.switch.on')}
      </Button>
    {/if}
    {#if plugState === 'on' || plugState === 'unknown'}
      <Button
        size="sm"
        variant="destructive"
        aria-label={t('power.switch.offOf', { printer })}
        disabled={busy || locked}
        onclick={turnOff}
      >
        {t('power.switch.off')}
      </Button>
    {/if}
  </div>
{:else}
  <p
    class="text-muted-foreground text-xs text-balance"
    data-part="no-operate"
    title={t('power.rights.operateNote')}
  >
    {t('power.rights.operateShort')}
  </p>
{/if}

<AlertDialog
  open={asking}
  onOpenChange={(next: boolean) => {
    asking = next;
  }}
>
  <AlertDialogContent data-module={OWN_MODULE}>
    <AlertDialogHeader>
      <AlertDialogTitle>{t('power.switch.ask', { printer })}</AlertDialogTitle>
      <AlertDialogDescription>
        {t('power.switch.askNote')}
      </AlertDialogDescription>
    </AlertDialogHeader>
    <AlertDialogFooter>
      <AlertDialogCancel>{t('power.switch.cancel')}</AlertDialogCancel>
      <AlertDialogAction variant="destructive" onclick={() => void send('off')}>
        {t('power.switch.confirm')}
      </AlertDialogAction>
    </AlertDialogFooter>
  </AlertDialogContent>
</AlertDialog>
