import type { Operation } from '@prusa/connect';
import type { ModuleHost } from '@prusa/connect-ui/slots';
import { OWN_MODULE } from './own-module';

const BASE = `/api/v1/modules/${OWN_MODULE || 'com.github.aux.prusa3d.tasmota-power'}`;

export type PlugDriver =
  | 'tasmota'
  | 'tasmota_http'
  | 'shelly_gen1'
  | 'shelly_gen2'
  | 'shelly_http'
  | 'zigbee2mqtt'
  | 'esphome'
  | 'openbeken'
  | 'generic_mqtt'
  | 'homeassistant';

export interface HAEntity {
  entity_id: string;
  friendly_name?: string | undefined;
  state?: string | undefined;
  domain?: string | undefined;
  device_class?: string | undefined;
  unit?: string | undefined;
}

export interface HATestResult {
  ok: boolean;
  url?: string | undefined;
  // The process answers null, not an empty list, when it found none.
  entities?: HAEntity[] | null | undefined;
  error?: string | undefined;
}

export interface PlugSettings {
  mqtt_builtin: boolean;
  mqtt_host: string;
  mqtt_port: number;
  mqtt_tls: boolean;
  mqtt_username: string;
  mqtt_password?: string | undefined;
  has_password: boolean;
  keep_password?: boolean | undefined;
  mqtt_client_id: string;
  ha_url?: string | undefined;
  ha_token?: string | undefined;
  has_ha_token?: boolean | undefined;
  keep_ha_token?: boolean | undefined;
  auto_off_cooldown_sec: number;
  confirm_turn_off: boolean;
}

export interface SettingsResponse {
  settings: PlugSettings;
  mqtt_configured: boolean;
  mqtt_connected: boolean;
  mqtt_broker?: string | undefined;
  mqtt_error?: string | undefined;
  mqtt_warning?: string | undefined;
  mqtt_builtin_running: boolean;
  mqtt_builtin_port?: number | undefined;
  mqtt_builtin_clients: number;
  /** The operator approved the built-in broker's port. */
  mqtt_builtin_granted?: boolean | undefined;
  ha_configured: boolean;
  /** Written in the host's own settings for this extension, so read-only here. */
  mqtt_host_pinned?: boolean | undefined;
  ha_url_pinned?: boolean | undefined;
  /** The address offered when a plug is set up by IP address. */
  suggested_http_host?: string | undefined;
}

export interface BrokerTestResult {
  ok: boolean;
  broker?: string | undefined;
  error?: string | undefined;
  warning?: string | undefined;
}

export interface DeviceConfig {
  driver?: PlugDriver | '' | undefined;
  mode?: 'mqtt' | 'http' | 'ha' | '' | undefined;
  tasmota_topic: string;
  http_host?: string | undefined;
  http_user?: string | undefined;
  http_password?: string | undefined;
  has_http_password?: boolean | undefined;
  keep_http_password?: boolean | undefined;
  mqtt_command_topic?: string | undefined;
  mqtt_state_topic?: string | undefined;
  mqtt_availability_topic?: string | undefined;
  mqtt_payload_on?: string | undefined;
  mqtt_payload_off?: string | undefined;
  mqtt_state_on?: string | undefined;
  mqtt_state_off?: string | undefined;
  mqtt_state_json_key?: string | undefined;
  mqtt_power_topic?: string | undefined;
  mqtt_power_json_key?: string | undefined;
  ha_entity_id?: string | undefined;
  ha_power_entity_id?: string | undefined;
  relay_index?: number | undefined;
  /** The plug turns off after the print that runs now, or the next one. */
  auto_off_after_print: boolean;
  /** The plug turns off after every print. */
  auto_off_default?: boolean | undefined;
  cooldown_sec?: number | undefined;
}

export function effectiveDriver(
  cfg: DeviceConfig | undefined,
): PlugDriver | '' {
  if (!cfg) return '';
  if (cfg.driver) return cfg.driver;
  if (cfg.mode === 'http') return 'tasmota_http';
  if (cfg.ha_entity_id) return 'homeassistant';
  if (cfg.mqtt_command_topic) return 'generic_mqtt';
  if (cfg.http_host && !cfg.tasmota_topic) return 'tasmota_http';
  if (cfg.tasmota_topic) return 'tasmota';
  return '';
}

export function isDeviceConfigured(cfg: DeviceConfig | undefined): boolean {
  if (!cfg) return false;
  const d = effectiveDriver(cfg);
  if (!d) return false;
  switch (d) {
    case 'tasmota_http':
    case 'shelly_http':
      return Boolean(cfg.http_host?.trim());
    case 'homeassistant':
      return Boolean(cfg.ha_entity_id?.trim());
    case 'generic_mqtt':
      return Boolean(cfg.mqtt_command_topic?.trim());
    default:
      return Boolean(cfg.tasmota_topic.trim());
  }
}

/** The address a person knows the plug by: its topic, its host or its entity. */
export function driverDisplayTarget(cfg: DeviceConfig | undefined): string {
  if (!cfg) return '';
  const d = effectiveDriver(cfg);
  switch (d) {
    case 'tasmota_http':
    case 'shelly_http':
      return cfg.http_host ?? '';
    case 'homeassistant':
      return cfg.ha_entity_id ?? '';
    case 'generic_mqtt':
      return cfg.mqtt_command_topic ?? '';
    default:
      return cfg.tasmota_topic;
  }
}

/** The relay a config names. An absent relay, or a zero, is the first. */
export const relayIndexOf = (cfg: DeviceConfig | undefined): number => {
  const relay = cfg?.relay_index;

  return relay === undefined || relay < 1 ? 1 : relay;
};

/** A text that says something, or `fallback` for an absent or empty one. */
export const textOr = (value: string | undefined, fallback: string): string =>
  value === undefined || value === '' ? fallback : value;

export type PowerState = 'ON' | 'OFF' | 'UNKNOWN';

export interface PlugTelemetry {
  topic?: string;
  driver?: string;
  friendly_name?: string;
  model?: string;
  relay_count?: number;
  relay_names?: string[];
  relays?: Record<string, PowerState>;
  power: PowerState;
  // The process answers null, not an absent key, for what a plug never said.
  online?: boolean | null;
  power_watts?: number | null;
  voltage?: number | null;
  current_amps?: number | null;
  last_error?: string;
  updated_at?: string;
}

/** A reading a plug gave, or undefined for one it never gave. Never a zero. */
export const readingOf = (
  value: number | null | undefined,
): number | undefined => value ?? undefined;

export interface PowerSample {
  ts: number;
  watts: number;
}

export interface PowerSession {
  active: boolean;
  started_at: string;
  ended_at?: string;
  duration_sec: number;
  energy_wh: number;
  avg_watts: number;
  peak_watts: number;
}

export interface PrintPowerRecord {
  job_id?: string;
  file_name: string;
  active: boolean;
  outcome?: string;
  started_at: string;
  ended_at?: string;
  duration_sec: number;
  energy_wh: number;
  avg_watts: number;
  peak_watts: number;
}

export interface PowerStatus {
  device_id: string;
  mqtt_configured: boolean;
  mqtt_connected: boolean;
  mqtt_broker?: string;
  mqtt_error?: string;
  ha_configured?: boolean;
  config: DeviceConfig;
  plug?: PlugTelemetry;
  discovered_plugs?: PlugTelemetry[] | null;
  can_turn_off: boolean;
  lock_reason?: string;
  printer_connected: boolean;
  printer_state?: string;
  printer_moving?: boolean;
  auto_off_pending: boolean;
  auto_off_remaining_sec: number;
  default_cooldown_sec: number;
  confirm_turn_off?: boolean;
  poll_interval_sec?: number;
  power_history?: PowerSample[];
  session_usage?: PowerSession;
  current_print_usage?: PrintPowerRecord;
  print_history?: PrintPowerRecord[];
}

export interface AssignmentsResponse {
  assignments: Record<string, DeviceConfig>;
  // The process answers null, not an empty list, for a list with nothing in it.
  discovered_plugs: PlugTelemetry[] | null;
  mqtt_configured: boolean;
  mqtt_connected: boolean;
  mqtt_broker?: string;
  mqtt_error?: string;
  ha_configured?: boolean;
  ha_entities?: HAEntity[] | null;
  default_cooldown_sec: number;
}

/** One printer's plug in the fleet overview. */
export interface OverviewEntry {
  device_id: string;
  config: DeviceConfig;
  plug?: PlugTelemetry;
  session_usage?: PowerSession;
  current_print_usage?: PrintPowerRecord;
}

/** Every printer with a plug the person may see, in one answer. */
export interface OverviewResponse {
  devices: OverviewEntry[];
  mqtt_configured: boolean;
  mqtt_connected: boolean;
  mqtt_error?: string;
  ha_configured: boolean;
  default_cooldown_sec: number;
  confirm_turn_off: boolean;
  poll_interval_sec: number;
}

const opGetSettings: Operation = {
  id: 'get-tasmota-settings',
  method: 'GET',
  path: `${BASE}/settings`,
  response: 'json',
  anonymous: false,
};

const opPutSettings: Operation = {
  id: 'put-tasmota-settings',
  method: 'PUT',
  path: `${BASE}/settings`,
  body: 'application/json',
  response: 'json',
  anonymous: false,
};

const opTestBroker: Operation = {
  id: 'test-broker-connection',
  method: 'POST',
  path: `${BASE}/broker/test`,
  body: 'application/json',
  response: 'json',
  anonymous: false,
};

const opTestHomeAssistant: Operation = {
  id: 'test-ha-connection',
  method: 'POST',
  path: `${BASE}/ha/test`,
  body: 'application/json',
  response: 'json',
  anonymous: false,
};

const opDiscover: Operation = {
  id: 'discover-tasmota-plugs',
  method: 'POST',
  path: `${BASE}/discover`,
  response: 'json',
  anonymous: false,
};

const opGetAssignments: Operation = {
  id: 'get-plug-assignments',
  method: 'GET',
  path: `${BASE}/assignments`,
  response: 'json',
  anonymous: false,
};

const opGetOverview: Operation = {
  id: 'get-power-overview',
  method: 'GET',
  path: `${BASE}/overview`,
  response: 'json',
  anonymous: false,
};

const opGetStatus: Operation = {
  id: 'get-device-power',
  method: 'GET',
  path: `${BASE}/devices/{device_id}`,
  response: 'json',
  anonymous: false,
};

const opPutConfig: Operation = {
  id: 'put-device-power-config',
  method: 'PUT',
  path: `${BASE}/devices/{device_id}/config`,
  body: 'application/json',
  response: 'json',
  anonymous: false,
};

const opPostPower: Operation = {
  id: 'post-device-power',
  method: 'POST',
  path: `${BASE}/devices/{device_id}/power`,
  body: 'application/json',
  response: 'json',
  anonymous: false,
};

const opPostAutoOff: Operation = {
  id: 'post-device-auto-off',
  method: 'POST',
  path: `${BASE}/devices/{device_id}/auto-off`,
  body: 'application/json',
  response: 'json',
  anonymous: false,
};

export function createApi(host: Pick<ModuleHost, 'client'>) {
  const t = host.client.transport;
  return {
    getSettings: (signal?: AbortSignal) =>
      t.call<SettingsResponse>(opGetSettings, {}, signal),

    saveSettings: (settings: Partial<PlugSettings>) =>
      t.call<SettingsResponse>(opPutSettings, { body: settings }),

    testBroker: (settings?: Partial<PlugSettings>) =>
      t.call<BrokerTestResult>(opTestBroker, { body: settings ?? {} }),

    testHomeAssistant: (settings?: Partial<PlugSettings>) =>
      t.call<HATestResult>(opTestHomeAssistant, { body: settings ?? {} }),

    discoverPlugs: () => t.call<{ plugs: PlugTelemetry[] }>(opDiscover, {}),

    getAssignments: (signal?: AbortSignal) =>
      t.call<AssignmentsResponse>(opGetAssignments, {}, signal),

    getOverview: (signal?: AbortSignal) =>
      t.call<OverviewResponse>(opGetOverview, {}, signal),

    getStatus: (deviceId: string, signal?: AbortSignal) =>
      t.call<PowerStatus>(
        opGetStatus,
        { path: { device_id: deviceId } },
        signal,
      ),

    saveConfig: (deviceId: string, config: DeviceConfig) =>
      t.call<PowerStatus>(opPutConfig, {
        path: { device_id: deviceId },
        body: config,
      }),

    sendPower: (deviceId: string, action: 'on' | 'off' | 'toggle') =>
      t.call<PowerStatus>(opPostPower, {
        path: { device_id: deviceId },
        body: { action },
      }),

    /** `asDefault` sets what the printer does after every print. */
    setAutoOff: (deviceId: string, enabled: boolean, asDefault = false) =>
      t.call<PowerStatus>(opPostAutoOff, {
        path: { device_id: deviceId },
        body: asDefault ? { enabled, default: enabled } : { enabled },
      }),
  };
}

export type PowerApi = ReturnType<typeof createApi>;
