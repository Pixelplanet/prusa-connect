<!--
  The last step of setting up a plug: the fields the chosen driver needs and
  no others. A plug the broker found, or an entity Home Assistant lists, is
  picked from a list that still takes a typed value.
-->
<script lang="ts">
  import {
    Button,
    Collapsible,
    CollapsibleContent,
    CollapsibleTrigger,
    Combobox,
    type ComboboxItem,
    Field,
    Input,
  } from '@prusa/connect-ui/primitives';

  import {
    effectiveDriver,
    type HAEntity,
    type PlugTelemetry,
    relayIndexOf,
  } from '../api';
  import {
    DRIVER_FIELDS,
    MAX_RELAYS,
    type PlugDraft,
    routeOf,
  } from '../drivers';
  import { heldHost } from '../host';
  import { typed } from '../input';
  import { STATE_LABELS } from '../plug-state';
  import { powerFor } from '../power.svelte';
  import ConnectionNote from './connection-note.svelte';
  import RelaySelect from './relay-select.svelte';

  let {
    draft = $bindable(),
    deviceId,
  }: {
    draft: PlugDraft;
    /** The printer the plug is for, which may keep the relay it has. */
    deviceId: string;
  } = $props();

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);

  let scanning = $state(false);
  // A second relay or an optional topic already in use opens the extras, so a
  // saved value is never out of sight behind a shut section.
  let more = $state(
    draft.relay > 1 ||
      [
        draft.mqttAvailTopic,
        draft.mqttPayloadOn,
        draft.mqttPayloadOff,
        draft.mqttStateJsonKey,
        draft.mqttPowerTopic,
        draft.mqttPowerJsonKey,
      ].some((value) => value !== ''),
  );

  const fields = $derived(DRIVER_FIELDS[draft.driver]);
  const transport = $derived(routeOf(draft.driver).transport);

  // The plugs the broker listed for this driver. Tasmota plugs carry no
  // driver name of their own.
  const found = $derived(
    !fields.discovery
      ? []
      : (power.assignments?.discovered_plugs ?? []).filter(
          (plug) =>
            plug.topic !== undefined &&
            (draft.driver === 'tasmota'
              ? plug.driver === undefined ||
                plug.driver === '' ||
                plug.driver === 'tasmota'
              : plug.driver === draft.driver),
        ),
  );
  const picked = $derived(
    found.find((plug) => plug.topic === draft.topic.trim()),
  );
  const mqttUp = $derived(
    power.settings?.mqtt_connected ?? power.overview?.mqtt_connected ?? false,
  );

  const wordOf = (plug: PlugTelemetry): string => {
    if (plug.online === false) return t(STATE_LABELS.offline);
    if ((plug.relay_count ?? 1) > 1) {
      return t('power.setup.plug.relays', { count: plug.relay_count ?? 1 });
    }
    if (plug.power === 'ON') return t(STATE_LABELS.on);
    if (plug.power === 'OFF') return t(STATE_LABELS.off);

    return t(STATE_LABELS.unknown);
  };

  const nameOf = (plug: PlugTelemetry): string => {
    const topic = plug.topic ?? '';

    return plug.friendly_name !== undefined &&
      plug.friendly_name !== '' &&
      plug.friendly_name !== topic
      ? t('power.setup.plug.named', { name: plug.friendly_name, topic })
      : topic;
  };

  /** The items of a picker, with what is typed kept as one of them. */
  const withTyped = (items: ComboboxItem[], value: string): ComboboxItem[] =>
    value === '' || items.some((item) => item.value === value)
      ? items
      : [...items, { value, label: value }];

  const plugItems = $derived(
    withTyped(
      found.map((plug) => ({
        value: plug.topic ?? '',
        label: t('power.setup.plug.option', {
          plug: nameOf(plug),
          state: wordOf(plug),
        }),
      })),
      draft.topic.trim(),
    ),
  );

  const entityItem = (entity: HAEntity): ComboboxItem => ({
    value: entity.entity_id,
    label:
      entity.friendly_name !== undefined && entity.friendly_name !== ''
        ? t('power.setup.plug.named', {
            name: entity.friendly_name,
            topic: entity.entity_id,
          })
        : entity.entity_id,
  });
  const switches = $derived(
    power.haEntities.filter(
      (entity) =>
        entity.domain === 'switch' || entity.domain === 'input_boolean',
    ),
  );
  const sensors = $derived(
    power.haEntities.filter((entity) => entity.domain === 'sensor'),
  );
  const switchItems = $derived(
    withTyped(switches.map(entityItem), draft.haEntityId.trim()),
  );
  const sensorItems = $derived(
    withTyped(sensors.map(entityItem), draft.haPowerEntityId.trim()),
  );

  // A plug whose relays the broker counted is offered exactly those. One
  // nothing is known about is offered the most a driver has, under the extras.
  const relayCount = $derived(picked?.relay_count);
  const relayUpFront = $derived(
    fields.relays && relayCount !== undefined && relayCount > 1,
  );
  const relayInExtras = $derived(fields.relays && relayCount === undefined);

  const useLabel = (text: string): string =>
    t('power.setup.plug.use', { text });

  /** The first relay of a plug that no other printer has. */
  const freeRelay = (topic: string): number => {
    const count = found.find((plug) => plug.topic === topic)?.relay_count ?? 1;
    const taken = Object.entries(power.assignments?.assignments ?? {})
      .filter(
        ([id, saved]) =>
          id !== deviceId &&
          effectiveDriver(saved) === draft.driver &&
          saved.tasmota_topic === topic,
      )
      .map(([, saved]) => relayIndexOf(saved));
    for (let relay = 1; relay <= count; relay += 1) {
      if (!taken.includes(relay)) return relay;
    }

    return 1;
  };

  const scan = async (): Promise<void> => {
    scanning = true;
    try {
      await power.scan();
    } catch {
      // A scan that fails leaves the list as it was. The connection note
      // above already says when the broker is not there.
    } finally {
      scanning = false;
    }
  };
</script>

<div class="flex flex-col gap-4">
  <ConnectionNote {transport} />

  {#if fields.address === 'topic'}
    {#if found.length > 0}
      <Field
        label={t(fields.label)}
        description={t('power.setup.plug.pickHint', {
          count: found.length,
        })}
        control={Combobox}
        controlProps={{
          items: plugItems,
          value: draft.topic.trim() === '' ? undefined : draft.topic.trim(),
          onPick: (value: string | undefined) => {
            draft.topic = value ?? '';
            draft.relay = freeRelay(draft.topic);
          },
          onCreate: (text: string) => {
            draft.topic = text.trim();
          },
          createLabel: useLabel,
          label: t(fields.label),
          placeholder: t(fields.example),
        }}
      />
    {:else}
      <Field
        label={t(fields.label)}
        description={t(fields.hint)}
        control={Input}
        controlProps={{
          value: draft.topic,
          oninput: typed((value) => (draft.topic = value)),
          placeholder: t(fields.example),
          autocomplete: 'off',
        }}
      />
    {/if}
    {#if fields.discovery && mqttUp}
      <div>
        <Button
          variant="secondary"
          size="sm"
          pending={scanning}
          onclick={() => void scan()}
        >
          {t('power.setup.plug.scan')}
        </Button>
      </div>
    {/if}
  {:else if fields.address === 'host'}
    <Field
      label={t(fields.label)}
      description={t(fields.hint)}
      control={Input}
      controlProps={{
        value: draft.httpHost,
        oninput: typed((value) => (draft.httpHost = value)),
        placeholder: t(fields.example),
        autocomplete: 'off',
      }}
    />
    <Field
      label={t('power.field.password.label')}
      description={t('power.field.password.hint')}
      control={Input}
      controlProps={{
        type: 'password',
        value: draft.httpPassword,
        oninput: typed((value) => (draft.httpPassword = value)),
        placeholder: draft.hasHttpPassword
          ? t('power.field.secretSaved')
          : t('power.field.optional'),
        autocomplete: 'new-password',
      }}
    />
  {:else if fields.address === 'entity'}
    {#if switches.length > 0}
      <Field
        label={t(fields.label)}
        description={t(fields.hint)}
        control={Combobox}
        controlProps={{
          items: switchItems,
          value:
            draft.haEntityId.trim() === ''
              ? undefined
              : draft.haEntityId.trim(),
          onPick: (value: string | undefined) => {
            draft.haEntityId = value ?? '';
          },
          onCreate: (text: string) => {
            draft.haEntityId = text.trim();
          },
          createLabel: useLabel,
          label: t(fields.label),
          placeholder: t(fields.example),
        }}
      />
    {:else}
      <Field
        label={t(fields.label)}
        description={t(fields.hint)}
        control={Input}
        controlProps={{
          value: draft.haEntityId,
          oninput: typed((value) => (draft.haEntityId = value)),
          placeholder: t(fields.example),
          autocomplete: 'off',
        }}
      />
    {/if}
    {#if sensors.length > 0}
      <Field
        label={t('power.field.powerEntity.label')}
        description={t('power.field.powerEntity.hint')}
        control={Combobox}
        controlProps={{
          items: sensorItems,
          value:
            draft.haPowerEntityId.trim() === ''
              ? undefined
              : draft.haPowerEntityId.trim(),
          onPick: (value: string | undefined) => {
            draft.haPowerEntityId = value ?? '';
          },
          onCreate: (text: string) => {
            draft.haPowerEntityId = text.trim();
          },
          createLabel: useLabel,
          noneLabel: t('power.field.powerEntity.none'),
          label: t('power.field.powerEntity.label'),
          placeholder: t('power.field.powerEntity.example'),
        }}
      />
    {:else}
      <Field
        label={t('power.field.powerEntity.label')}
        description={t('power.field.powerEntity.hint')}
        control={Input}
        controlProps={{
          value: draft.haPowerEntityId,
          oninput: typed((value) => (draft.haPowerEntityId = value)),
          placeholder: t('power.field.powerEntity.example'),
          autocomplete: 'off',
        }}
      />
    {/if}
  {:else}
    <Field
      label={t(fields.label)}
      description={t(fields.hint)}
      control={Input}
      controlProps={{
        value: draft.mqttCmdTopic,
        oninput: typed((value) => (draft.mqttCmdTopic = value)),
        placeholder: t(fields.example),
        autocomplete: 'off',
      }}
    />
    <Field
      label={t('power.field.stateTopic.label')}
      description={t('power.field.stateTopic.hint')}
      control={Input}
      controlProps={{
        value: draft.mqttStateTopic,
        oninput: typed((value) => (draft.mqttStateTopic = value)),
        placeholder: t('power.field.stateTopic.example'),
        autocomplete: 'off',
      }}
    />
  {/if}

  {#if relayUpFront}
    <RelaySelect
      value={draft.relay}
      count={relayCount ?? 1}
      onchange={(relay: number) => (draft.relay = relay)}
    />
  {/if}

  {#if relayInExtras || fields.address === 'command'}
    <Collapsible bind:open={more}>
      <CollapsibleTrigger variant="inline">
        {t('power.setup.plug.more')}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div class="flex flex-col gap-4 pt-3" data-part="more-options">
          {#if relayInExtras}
            <RelaySelect
              value={draft.relay}
              count={MAX_RELAYS}
              onchange={(relay: number) => (draft.relay = relay)}
            />
          {/if}
          {#if fields.address === 'command'}
            <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field
                label={t('power.field.payloadOn.label')}
                control={Input}
                controlProps={{
                  value: draft.mqttPayloadOn,
                  oninput: typed((value) => (draft.mqttPayloadOn = value)),
                  placeholder: t('power.field.payloadOn.example'),
                  autocomplete: 'off',
                }}
              />
              <Field
                label={t('power.field.payloadOff.label')}
                control={Input}
                controlProps={{
                  value: draft.mqttPayloadOff,
                  oninput: typed((value) => (draft.mqttPayloadOff = value)),
                  placeholder: t('power.field.payloadOff.example'),
                  autocomplete: 'off',
                }}
              />
            </div>
            <Field
              label={t('power.field.stateKey.label')}
              description={t('power.field.stateKey.hint')}
              control={Input}
              controlProps={{
                value: draft.mqttStateJsonKey,
                oninput: typed((value) => (draft.mqttStateJsonKey = value)),
                placeholder: t('power.field.stateKey.example'),
                autocomplete: 'off',
              }}
            />
            <Field
              label={t('power.field.availability.label')}
              description={t('power.field.availability.hint')}
              control={Input}
              controlProps={{
                value: draft.mqttAvailTopic,
                oninput: typed((value) => (draft.mqttAvailTopic = value)),
                placeholder: t('power.field.availability.example'),
                autocomplete: 'off',
              }}
            />
            <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field
                label={t('power.field.powerTopic.label')}
                control={Input}
                controlProps={{
                  value: draft.mqttPowerTopic,
                  oninput: typed((value) => (draft.mqttPowerTopic = value)),
                  placeholder: t('power.field.powerTopic.example'),
                  autocomplete: 'off',
                }}
              />
              <Field
                label={t('power.field.powerKey.label')}
                control={Input}
                controlProps={{
                  value: draft.mqttPowerJsonKey,
                  oninput: typed((value) => (draft.mqttPowerJsonKey = value)),
                  placeholder: t('power.field.powerKey.example'),
                  autocomplete: 'off',
                }}
              />
            </div>
          {/if}
        </div>
      </CollapsibleContent>
    </Collapsible>
  {/if}
</div>
