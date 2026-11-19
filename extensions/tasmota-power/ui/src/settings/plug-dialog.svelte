<!--
  Sets up or changes the plug of one printer, as three questions: which plug
  it is, how Connect reaches it, and where it is. The second question is left
  out for a plug with one answer to it. After the last one the dialog waits
  for the plug and says whether it answered.

  The parent mounts this while a printer is being set up, so every opening
  starts from what is saved.
-->
<script lang="ts">
  import {
    Alert,
    AlertDescription,
    Button,
    GuidedDialog,
    type GuidedStatus,
    Label,
    RadioGroup,
    RadioGroupItem,
    Tile,
  } from '@prusa/connect-ui/primitives';
  import { onMount } from 'svelte';

  import { isDeviceConfigured, type PowerStatus, readingOf } from '../api';
  import {
    configOf,
    draftComplete,
    draftOf,
    kindOf,
    namesSamePlug,
    PLUG_KINDS,
    type PlugKindInfo,
    routeOf,
  } from '../drivers';
  import FaultNote from '../fault-note.svelte';
  import { formatWatts } from '../format';
  import { heldHost } from '../host';
  import { OWN_MODULE } from '../own-module';
  import { plugStateOf } from '../plug-state';
  import { powerFor } from '../power.svelte';
  import ConnectionNote from './connection-note.svelte';
  import PlugFields from './plug-fields.svelte';

  let {
    device,
    onclose,
  }: {
    device: { id: string; name: string };
    onclose: () => void;
  } = $props();

  type StepId = 'type' | 'route' | 'plug';

  /** How long the dialog waits for a plug on the broker to answer. */
  const ANSWER_TRIES = 6;
  const ANSWER_WAIT_MS = 1000;

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);
  const uid = $props.id();

  // What is saved when the dialog opens. The dialog is mounted per opening,
  // so these are read once on purpose.
  // svelte-ignore state_referenced_locally
  const saved =
    power.entry(device.id)?.config ?? power.assignments?.assignments[device.id];
  const changing = isDeviceConfigured(saved);

  let draft = $state(draftOf(saved, power.settings?.suggested_http_host ?? ''));
  // svelte-ignore state_referenced_locally
  let kind = $state.raw<PlugKindInfo | undefined>(
    changing ? kindOf(draft.driver) : undefined,
  );
  let step = $state<StepId>(changing ? 'plug' : 'type');
  let status = $state<GuidedStatus>('editing');
  let fault = $state.raw<unknown>(undefined);
  let answer = $state.raw<PowerStatus | undefined>(undefined);
  let gone = false;

  const stepIds = $derived<StepId[]>(
    (kind?.routes.length ?? 0) > 1
      ? ['type', 'route', 'plug']
      : ['type', 'plug'],
  );
  const steps = $derived(
    stepIds.map((id) => ({ id, label: t(`power.setup.step.${id}`) })),
  );
  const current = $derived(Math.max(0, stepIds.indexOf(step)));

  /** The other printer that already has the plug and relay the draft names. */
  const takenBy = $derived.by((): string | undefined => {
    const others = Object.entries(power.assignments?.assignments ?? {});
    const other = others.find(
      ([id, config]) => id !== device.id && namesSamePlug(draft, config),
    );
    if (other === undefined) return undefined;

    return (
      host.fleet.device(other[0])?.name ?? t('power.setup.plug.otherPrinter')
    );
  });

  const canContinue = $derived.by((): boolean => {
    if (step === 'type') return kind !== undefined;
    if (step === 'plug') return draftComplete(draft) && takenBy === undefined;

    return true;
  });

  const answered = (result: PowerStatus): boolean => {
    const state = plugStateOf(result.config, result.plug);

    return state === 'on' || state === 'off';
  };

  const endMessage = $derived.by((): string => {
    if (answer === undefined) return '';
    if (status === 'failure') {
      return answer.plug?.last_error
        ? t('power.setup.end.silentBecause', {
            printer: device.name,
            reason: answer.plug.last_error,
          })
        : t('power.setup.end.silent', { printer: device.name });
    }
    if (answer.plug?.power !== 'ON') return t('power.setup.end.off');

    const watts = readingOf(answer.plug.power_watts);

    return watts === undefined
      ? t('power.setup.end.on')
      : t('power.setup.end.onWatts', { watts: formatWatts(watts) });
  });

  const pickKind = (info: PlugKindInfo): void => {
    kind = info;
    if (!info.routes.some((route) => route.driver === draft.driver)) {
      draft.driver = info.routes[0]?.driver ?? draft.driver;
    }
    step = info.routes.length > 1 ? 'route' : 'plug';
  };

  /** Takes a route the picked kind lists, and no other value. */
  const pickRoute = (value: string): void => {
    const route = kind?.routes.find((one) => one.driver === value);
    if (route !== undefined) draft.driver = route.driver;
  };

  const pause = (ms: number): Promise<void> =>
    new Promise((resolve) => {
      setTimeout(resolve, ms);
    });

  const finish = async (): Promise<void> => {
    status = 'waiting';
    fault = undefined;
    try {
      let result = await power.saveConfig(device.id, configOf(draft, saved));
      // A plug on the broker answers a moment after it is asked. One reached
      // by its address was already asked while it was saved.
      for (
        let attempt = 0;
        attempt < ANSWER_TRIES && !answered(result) && !gone;
        attempt += 1
      ) {
        await pause(ANSWER_WAIT_MS);
        result = await power.api.getStatus(device.id);
      }
      answer = result;
      status = answered(result) ? 'success' : 'failure';
    } catch (thrown) {
      fault = thrown;
      status = 'editing';
    }
  };

  const next = (): void => {
    const following = stepIds[current + 1];
    if (following === undefined) void finish();
    else step = following;
  };

  const back = (): void => {
    step = stepIds[current - 1] ?? 'type';
  };

  onMount(() => () => {
    gone = true;
  });
</script>

<GuidedDialog
  open
  title={changing
    ? t('power.setup.titleChange', { printer: device.name })
    : t('power.setup.title', { printer: device.name })}
  {steps}
  {current}
  {status}
  {canContinue}
  finishLabel={t('power.setup.save')}
  waitingMessage={t('power.setup.waiting')}
  endTitle={status === 'success'
    ? t('power.setup.end.answered')
    : t('power.setup.end.noAnswer')}
  {endMessage}
  onnext={next}
  onback={back}
  {onclose}
  {...status === 'failure' ? { endActions: failureActions } : {}}
>
  <!-- The dialog is drawn outside this module's slot, so its content names
       the module for the module's own utilities to reach it. -->
  <div data-module={OWN_MODULE} class="flex flex-col gap-4 p-1">
    {#if step !== 'type' && kind !== undefined}
      <div class="flex flex-col gap-1 text-sm" data-part="answers">
        <div class="flex items-center justify-between gap-2">
          <span>
            {t('power.setup.answer', {
              question: t('power.setup.step.type'),
              answer: t(kind.name),
            })}
          </span>
          <Button variant="link" onclick={() => (step = 'type')}>
            {t('power.setup.changeAnswer')}
          </Button>
        </div>
        {#if step === 'plug' && kind.routes.length > 1}
          <div class="flex items-center justify-between gap-2">
            <span>
              {t('power.setup.answer', {
                question: t('power.setup.step.route'),
                answer: t(routeOf(draft.driver).label),
              })}
            </span>
            <Button variant="link" onclick={() => (step = 'route')}>
              {t('power.setup.changeAnswer')}
            </Button>
          </div>
        {/if}
      </div>
    {/if}
    {#if step === 'type'}
      <p class="text-muted-foreground text-sm">
        {t('power.setup.type.question')}
      </p>
      <ul class="grid grid-cols-1 gap-2 sm:grid-cols-2" data-part="plug-kinds">
        {#each PLUG_KINDS as info (info.kind)}
          <li>
            <Tile
              size="sm"
              class="h-full"
              pressed={kind?.kind === info.kind}
              data-kind={info.kind}
              onclick={() => {
                pickKind(info);
              }}
            >
              <span class="flex flex-col gap-0.5">
                <span class="text-sm font-medium">{t(info.name)}</span>
                <span class="text-muted-foreground text-xs">
                  {t(info.hint)}
                </span>
              </span>
            </Tile>
          </li>
        {/each}
      </ul>
    {:else if step === 'route'}
      <p class="text-muted-foreground text-sm">
        {t('power.setup.route.question')}
      </p>
      <RadioGroup
        value={draft.driver}
        aria-label={t('power.setup.route.question')}
        onValueChange={pickRoute}
      >
        {#each kind?.routes ?? [] as route (route.driver)}
          <div class="flex items-start gap-3 py-1" data-route={route.driver}>
            <RadioGroupItem
              value={route.driver}
              id={`${uid}-${route.driver}`}
              class="mt-0.5"
            />
            <div class="flex min-w-0 flex-col gap-0.5">
              <Label for={`${uid}-${route.driver}`} variant="option">
                {t(route.label)}
              </Label>
              <p class="text-muted-foreground text-xs">{t(route.hint)}</p>
            </div>
          </div>
        {/each}
      </RadioGroup>
      <ConnectionNote transport={routeOf(draft.driver).transport} />
    {:else}
      <PlugFields bind:draft deviceId={device.id} />
      {#if takenBy !== undefined}
        <Alert variant="destructive" data-part="plug-taken">
          <AlertDescription>
            {t('power.setup.plug.taken', { printer: takenBy })}
          </AlertDescription>
        </Alert>
      {/if}
    {/if}
    <FaultNote {fault} />
  </div>
</GuidedDialog>

{#snippet failureActions()}
  <Button
    variant="secondary"
    onclick={() => {
      status = 'editing';
    }}
  >
    {t('power.setup.end.change')}
  </Button>
  <Button onclick={onclose}>{t('power.setup.end.keep')}</Button>
{/snippet}
