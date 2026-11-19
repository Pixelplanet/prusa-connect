// What a person picks when they set up a plug, and how that maps to the ten
// drivers the process speaks. A person answers two questions: which plug they
// have, and how Connect reaches it. The pair names one driver.
//
// Every text here is a key of the catalogue, never a sentence.
import {
  type DeviceConfig,
  effectiveDriver,
  type PlugDriver,
  relayIndexOf,
  textOr,
} from './api';

/** What the plug is: its firmware, or the system that controls it. */
export type PlugKind =
  | 'tasmota'
  | 'shelly'
  | 'zigbee2mqtt'
  | 'esphome'
  | 'openbeken'
  | 'homeassistant'
  | 'generic';

/** How Connect reaches a plug. */
export type Transport = 'mqtt' | 'http' | 'ha';

/** One way to reach a kind of plug, which is one driver. */
export interface PlugRoute {
  readonly driver: PlugDriver;
  readonly transport: Transport;
  readonly label: string;
  readonly hint: string;
}

export interface PlugKindInfo {
  readonly kind: PlugKind;
  readonly name: string;
  readonly hint: string;
  readonly routes: readonly PlugRoute[];
}

const mqttOnly = (driver: PlugDriver): readonly PlugRoute[] => [
  {
    driver,
    transport: 'mqtt',
    label: 'power.route.mqtt.label',
    hint: 'power.route.mqtt.hint',
  },
];

export const PLUG_KINDS: readonly PlugKindInfo[] = [
  {
    kind: 'tasmota',
    name: 'power.plugType.tasmota.name',
    hint: 'power.plugType.tasmota.hint',
    routes: [
      {
        driver: 'tasmota',
        transport: 'mqtt',
        label: 'power.route.mqtt.label',
        hint: 'power.route.mqtt.hint',
      },
      {
        driver: 'tasmota_http',
        transport: 'http',
        label: 'power.route.http.label',
        hint: 'power.route.http.hint',
      },
    ],
  },
  {
    kind: 'shelly',
    name: 'power.plugType.shelly.name',
    hint: 'power.plugType.shelly.hint',
    routes: [
      {
        driver: 'shelly_http',
        transport: 'http',
        label: 'power.route.http.label',
        hint: 'power.route.shellyHttp.hint',
      },
      {
        driver: 'shelly_gen2',
        transport: 'mqtt',
        label: 'power.route.shellyGen2.label',
        hint: 'power.route.shellyGen2.hint',
      },
      {
        driver: 'shelly_gen1',
        transport: 'mqtt',
        label: 'power.route.shellyGen1.label',
        hint: 'power.route.shellyGen1.hint',
      },
    ],
  },
  {
    kind: 'zigbee2mqtt',
    name: 'power.plugType.zigbee2mqtt.name',
    hint: 'power.plugType.zigbee2mqtt.hint',
    routes: mqttOnly('zigbee2mqtt'),
  },
  {
    kind: 'esphome',
    name: 'power.plugType.esphome.name',
    hint: 'power.plugType.esphome.hint',
    routes: mqttOnly('esphome'),
  },
  {
    kind: 'openbeken',
    name: 'power.plugType.openbeken.name',
    hint: 'power.plugType.openbeken.hint',
    routes: mqttOnly('openbeken'),
  },
  {
    kind: 'homeassistant',
    name: 'power.plugType.homeassistant.name',
    hint: 'power.plugType.homeassistant.hint',
    routes: [
      {
        driver: 'homeassistant',
        transport: 'ha',
        label: 'power.route.ha.label',
        hint: 'power.route.ha.hint',
      },
    ],
  },
  {
    kind: 'generic',
    name: 'power.plugType.generic.name',
    hint: 'power.plugType.generic.hint',
    routes: mqttOnly('generic_mqtt'),
  },
];

/** The kind a driver belongs to. Every driver is a route of exactly one. */
export const kindOf = (driver: PlugDriver): PlugKindInfo => {
  const found = PLUG_KINDS.find((info) =>
    info.routes.some((route) => route.driver === driver),
  );
  if (found === undefined) {
    throw new Error(`no plug kind lists the driver ${driver}`);
  }

  return found;
};

export const routeOf = (driver: PlugDriver): PlugRoute => {
  const found = kindOf(driver).routes.find((route) => route.driver === driver);
  if (found === undefined) {
    throw new Error(`no route lists the driver ${driver}`);
  }

  return found;
};

export const transportOf = (driver: PlugDriver | ''): Transport | undefined =>
  driver === '' ? undefined : routeOf(driver).transport;

/** The name of the transport, as one short word for a list. */
export const TRANSPORT_NAMES: Readonly<Record<Transport, string>> = {
  mqtt: 'power.transport.mqtt',
  http: 'power.transport.http',
  ha: 'power.transport.ha',
};

/** Which value names the plug: a topic, a host, an entity or a command topic. */
export type AddressKind = 'topic' | 'host' | 'entity' | 'command';

export interface DriverFields {
  readonly address: AddressKind;
  /** The label, the example and the hint of the address field. */
  readonly label: string;
  readonly example: string;
  readonly hint: string;
  /** The plug may have more than one relay. */
  readonly relays: boolean;
  /** The broker can list the plugs of this driver. */
  readonly discovery: boolean;
}

const topicFields = (
  name: string,
  relays: boolean,
  discovery = false,
): DriverFields => ({
  address: 'topic',
  label: `power.field.${name}.label`,
  example: `power.field.${name}.example`,
  hint: `power.field.${name}.hint`,
  relays,
  discovery,
});

const hostFields: DriverFields = {
  address: 'host',
  label: 'power.field.host.label',
  example: 'power.field.host.example',
  hint: 'power.field.host.hint',
  relays: true,
  discovery: false,
};

export const DRIVER_FIELDS: Readonly<Record<PlugDriver, DriverFields>> = {
  tasmota: topicFields('tasmota', true, true),
  shelly_gen1: topicFields('shellyGen1', true, true),
  shelly_gen2: topicFields('shellyGen2', true),
  zigbee2mqtt: topicFields('zigbee2mqtt', true),
  esphome: topicFields('esphome', false),
  openbeken: topicFields('openbeken', true),
  tasmota_http: hostFields,
  shelly_http: hostFields,
  generic_mqtt: {
    address: 'command',
    label: 'power.field.command.label',
    example: 'power.field.command.example',
    hint: 'power.field.command.hint',
    relays: false,
    discovery: false,
  },
  homeassistant: {
    address: 'entity',
    label: 'power.field.entity.label',
    example: 'power.field.entity.example',
    hint: 'power.field.entity.hint',
    relays: false,
    discovery: false,
  },
};

/** The most relays a plug is offered when the broker did not say how many it has. */
export const MAX_RELAYS = 8;

/** What a person is typing for one printer's plug, before it is saved. */
export interface PlugDraft {
  driver: PlugDriver;
  topic: string;
  relay: number;
  httpHost: string;
  httpPassword: string;
  hasHttpPassword: boolean;
  mqttCmdTopic: string;
  mqttStateTopic: string;
  mqttAvailTopic: string;
  mqttPayloadOn: string;
  mqttPayloadOff: string;
  mqttStateJsonKey: string;
  mqttPowerTopic: string;
  mqttPowerJsonKey: string;
  haEntityId: string;
  haPowerEntityId: string;
}

export const draftOf = (
  cfg: DeviceConfig | undefined,
  suggestedHost = '',
): PlugDraft => ({
  driver: effectiveDriver(cfg) || 'tasmota',
  topic: cfg?.tasmota_topic ?? '',
  relay: relayIndexOf(cfg),
  httpHost: textOr(cfg?.http_host, suggestedHost),
  httpPassword: '',
  hasHttpPassword: Boolean(cfg?.has_http_password),
  mqttCmdTopic: cfg?.mqtt_command_topic ?? '',
  mqttStateTopic: cfg?.mqtt_state_topic ?? '',
  mqttAvailTopic: cfg?.mqtt_availability_topic ?? '',
  mqttPayloadOn: cfg?.mqtt_payload_on ?? '',
  mqttPayloadOff: cfg?.mqtt_payload_off ?? '',
  mqttStateJsonKey: cfg?.mqtt_state_json_key ?? '',
  mqttPowerTopic: cfg?.mqtt_power_topic ?? '',
  mqttPowerJsonKey: cfg?.mqtt_power_json_key ?? '',
  haEntityId: cfg?.ha_entity_id ?? '',
  haPowerEntityId: cfg?.ha_power_entity_id ?? '',
});

/** The value that names the plug for the draft's driver. */
export const addressOf = (draft: PlugDraft): string => {
  switch (DRIVER_FIELDS[draft.driver].address) {
    case 'host':
      return draft.httpHost.trim();
    case 'entity':
      return draft.haEntityId.trim();
    case 'command':
      return draft.mqttCmdTopic.trim();
    default:
      return draft.topic.trim();
  }
};

export const draftComplete = (draft: PlugDraft): boolean =>
  addressOf(draft) !== '';

/** The relay a draft names, which is the first for a driver with one relay. */
export const relayOf = (draft: PlugDraft): number =>
  DRIVER_FIELDS[draft.driver].relays ? Math.max(1, draft.relay) : 1;

/**
 * The request that saves a draft. The auto-off choices are not part of a
 * plug's setup, so they are carried over from what is saved.
 */
export const configOf = (
  draft: PlugDraft,
  existing: DeviceConfig | undefined,
): DeviceConfig => {
  const body: DeviceConfig = {
    driver: draft.driver,
    tasmota_topic: '',
    auto_off_after_print: existing?.auto_off_after_print ?? false,
    cooldown_sec: existing?.cooldown_sec,
  };

  switch (DRIVER_FIELDS[draft.driver].address) {
    case 'host':
      body.mode = 'http';
      body.http_host = draft.httpHost.trim();
      body.http_password = draft.httpPassword;
      body.keep_http_password =
        draft.httpPassword === '' && draft.hasHttpPassword;
      body.relay_index = relayOf(draft);
      break;
    case 'entity':
      body.mode = 'ha';
      body.ha_entity_id = draft.haEntityId.trim();
      body.ha_power_entity_id = draft.haPowerEntityId.trim();
      break;
    case 'command':
      body.mode = 'mqtt';
      body.mqtt_command_topic = draft.mqttCmdTopic.trim();
      body.mqtt_state_topic = draft.mqttStateTopic.trim();
      body.mqtt_availability_topic = draft.mqttAvailTopic.trim();
      body.mqtt_payload_on = draft.mqttPayloadOn.trim();
      body.mqtt_payload_off = draft.mqttPayloadOff.trim();
      body.mqtt_state_json_key = draft.mqttStateJsonKey.trim();
      body.mqtt_power_topic = draft.mqttPowerTopic.trim();
      body.mqtt_power_json_key = draft.mqttPowerJsonKey.trim();
      break;
    default:
      body.mode = 'mqtt';
      body.tasmota_topic = draft.topic.trim();
      body.relay_index = relayOf(draft);
  }

  return body;
};

/** The request that takes a printer's plug away and keeps its auto-off choices. */
export const clearedConfig = (
  existing: DeviceConfig | undefined,
): DeviceConfig => ({
  driver: '',
  mode: '',
  tasmota_topic: '',
  http_host: '',
  http_password: '',
  keep_http_password: false,
  ha_entity_id: '',
  ha_power_entity_id: '',
  mqtt_command_topic: '',
  auto_off_after_print: existing?.auto_off_after_print ?? false,
  cooldown_sec: existing?.cooldown_sec,
});

/**
 * The request that changes one printer's auto-off choices and nothing of its
 * plug. A saved password stays saved: the listing never carries it.
 */
export const withAutoOff = (
  existing: DeviceConfig,
  changes: {
    auto_off_after_print?: boolean;
    cooldown_sec?: number | undefined;
  },
): DeviceConfig => ({
  ...existing,
  http_password: '',
  keep_http_password: Boolean(existing.has_http_password),
  auto_off_after_print:
    changes.auto_off_after_print ?? existing.auto_off_after_print,
  cooldown_sec:
    'cooldown_sec' in changes ? changes.cooldown_sec : existing.cooldown_sec,
});

/**
 * Whether a saved plug is the one a draft names: the same driver, the same
 * address and the same relay. One relay switches one printer.
 */
export const namesSamePlug = (
  draft: PlugDraft,
  saved: DeviceConfig,
): boolean => {
  if (effectiveDriver(saved) !== draft.driver) return false;
  const fields = DRIVER_FIELDS[draft.driver];
  const address = addressOf(draft).toLowerCase();
  if (address === '') return false;

  switch (fields.address) {
    case 'host':
      return (
        (saved.http_host ?? '').trim().toLowerCase() === address &&
        relayIndexOf(saved) === relayOf(draft)
      );
    case 'entity':
      return (saved.ha_entity_id ?? '').trim().toLowerCase() === address;
    case 'command':
      return (saved.mqtt_command_topic ?? '').trim().toLowerCase() === address;
    default:
      return (
        saved.tasmota_topic.trim().toLowerCase() === address &&
        relayIndexOf(saved) === relayOf(draft)
      );
  }
};
