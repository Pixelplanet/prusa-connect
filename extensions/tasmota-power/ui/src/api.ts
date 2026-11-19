import type { Operation } from '@prusa/connect';
import type { ModuleHost } from '@prusa/connect-ui/slots';

const BASE = '/api/v1/modules/com.github.aux.prusa3d.tasmota-power';

export interface TasmotaSettings {
  mqtt_host: string;
  mqtt_port: number;
  mqtt_tls: boolean;
  mqtt_username: string;
  mqtt_password?: string;
  has_password: boolean;
  keep_password?: boolean;
  mqtt_client_id: string;
  auto_off_cooldown_sec: number;
  confirm_turn_off: boolean;
}

export interface SettingsResponse {
  settings: TasmotaSettings;
  mqtt_configured: boolean;
  mqtt_connected: boolean;
  mqtt_broker?: string;
  mqtt_error?: string;
}

export interface BrokerTestResult {
  ok: boolean;
  broker?: string;
  error?: string;
}

export interface DeviceConfig {
  tasmota_topic: string;
  relay_index?: number;
  auto_off_after_print: boolean;
  cooldown_sec?: number;
}

export interface PlugTelemetry {
  topic?: string;
  friendly_name?: string;
  model?: string;
  relay_count?: number;
  relay_names?: string[];
  relays?: Record<string, 'ON' | 'OFF' | 'UNKNOWN'>;
  power: 'ON' | 'OFF' | 'UNKNOWN';
  online?: boolean;
  power_watts?: number;
  voltage?: number;
  current_amps?: number;
  last_error?: string;
  updated_at?: string;
}

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
  config: DeviceConfig;
  plug?: PlugTelemetry;
  discovered_plugs?: PlugTelemetry[];
  can_turn_off: boolean;
  lock_reason?: string;
  printer_connected: boolean;
  printer_state?: string;
  printer_moving?: boolean;
  auto_off_pending: boolean;
  auto_off_remaining_sec: number;
  default_cooldown_sec: number;
  confirm_turn_off?: boolean;
  power_history?: PowerSample[];
  session_usage?: PowerSession;
  current_print_usage?: PrintPowerRecord;
  print_history?: PrintPowerRecord[];
}

export interface AssignmentsResponse {
  assignments: Record<string, DeviceConfig>;
  discovered_plugs: PlugTelemetry[];
  mqtt_configured: boolean;
  mqtt_connected: boolean;
  mqtt_broker?: string;
  mqtt_error?: string;
  default_cooldown_sec: number;
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

    saveSettings: (settings: Partial<TasmotaSettings>) =>
      t.call<SettingsResponse>(opPutSettings, { body: settings }),

    testBroker: (settings?: Partial<TasmotaSettings>) =>
      t.call<BrokerTestResult>(opTestBroker, { body: settings ?? {} }),

    discoverPlugs: () =>
      t.call<{ plugs: PlugTelemetry[] }>(opDiscover, {}),

    getAssignments: (signal?: AbortSignal) =>
      t.call<AssignmentsResponse>(opGetAssignments, {}, signal),

    getStatus: (deviceId: string, signal?: AbortSignal) =>
      t.call<PowerStatus>(opGetStatus, { path: { device_id: deviceId } }, signal),

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

    setAutoOff: (deviceId: string, enabled: boolean) =>
      t.call<PowerStatus>(opPostAutoOff, {
        path: { device_id: deviceId },
        body: { enabled },
      }),
  };
}
