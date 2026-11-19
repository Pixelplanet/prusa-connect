// The one store every screen of this module reads: the printers' plugs, the
// connections and what the broker found.
//
// The overview is read once for every screen, however many show it. A page of
// forty printer cards and a dashboard with three power cards ask the process
// one question every few seconds between them, and nothing is asked while no
// screen is watching.
import type { ModuleHost } from '@prusa/connect-ui/slots';

import {
  type AssignmentsResponse,
  createApi,
  type DeviceConfig,
  effectiveDriver,
  type HAEntity,
  isDeviceConfigured,
  type OverviewEntry,
  type OverviewResponse,
  type PlugSettings,
  type PowerApi,
  type PowerStatus,
  type SettingsResponse,
} from './api';
import { type Transport, transportOf } from './drivers';

const POLL_MS = 5000;

/** What a settings pane changes. A secret left empty keeps the saved one. */
export type SettingsChanges = Partial<
  Pick<
    PlugSettings,
    | 'mqtt_builtin'
    | 'mqtt_host'
    | 'mqtt_port'
    | 'mqtt_tls'
    | 'mqtt_username'
    | 'mqtt_password'
    | 'mqtt_client_id'
    | 'ha_url'
    | 'ha_token'
    | 'auto_off_cooldown_sec'
    | 'confirm_turn_off'
  >
>;

export class PowerStore {
  readonly #api: PowerApi;

  #overview = $state.raw<OverviewResponse | undefined>(undefined);
  #overviewFault = $state.raw<unknown>(undefined);
  #settings = $state.raw<SettingsResponse | undefined>(undefined);
  #settingsFault = $state.raw<unknown>(undefined);
  #assignments = $state.raw<AssignmentsResponse | undefined>(undefined);
  #assignmentsFault = $state.raw<unknown>(undefined);
  #haEntities = $state.raw<readonly HAEntity[]>([]);

  #watchers = 0;
  #timer: ReturnType<typeof setInterval> | undefined;
  #reading = false;

  readonly #entries = $derived.by((): Readonly<Record<string, OverviewEntry>> =>
    Object.fromEntries(
      (this.#overview?.devices ?? []).map((entry) => [entry.device_id, entry]),
    ),
  );

  readonly #used = $derived.by((): Readonly<Record<Transport, number>> => {
    const counts = { mqtt: 0, http: 0, ha: 0 };
    for (const entry of this.#overview?.devices ?? []) {
      if (!isDeviceConfigured(entry.config)) continue;
      const transport = transportOf(effectiveDriver(entry.config));
      if (transport !== undefined) counts[transport] += 1;
    }

    return counts;
  });

  constructor(api: PowerApi, signal: AbortSignal) {
    this.#api = api;
    signal.addEventListener(
      'abort',
      () => {
        this.#stop();
      },
      { once: true },
    );
  }

  /** The requests a screen makes for itself: one printer's status, a test. */
  get api(): PowerApi {
    return this.#api;
  }

  /** Every printer's plug. Undefined until the first read answers. Read-only by design. */
  get overview(): OverviewResponse | undefined {
    return this.#overview;
  }

  /** Why the last read of the overview failed, cleared when one succeeds. */
  get overviewFault(): unknown {
    return this.#overviewFault;
  }

  get settings(): SettingsResponse | undefined {
    return this.#settings;
  }

  get settingsFault(): unknown {
    return this.#settingsFault;
  }

  get assignments(): AssignmentsResponse | undefined {
    return this.#assignments;
  }

  get assignmentsFault(): unknown {
    return this.#assignmentsFault;
  }

  /** The Home Assistant entities the last read or test answered. */
  get haEntities(): readonly HAEntity[] {
    return this.#haEntities;
  }

  /** How many plugs are reached each way, so a connection says who needs it. */
  get used(): Readonly<Record<Transport, number>> {
    return this.#used;
  }

  /** One printer's plug, or undefined for a printer with none. */
  entry(deviceId: string): OverviewEntry | undefined {
    return this.#entries[deviceId];
  }

  /**
   * Keeps the overview fresh while a screen shows it. A screen calls it when
   * it mounts and calls what it returns when it goes.
   */
  watch(): () => void {
    this.#watchers += 1;
    if (this.#watchers === 1) {
      void this.refresh();
      this.#timer = setInterval(() => {
        void this.refresh();
      }, POLL_MS);
    }

    let released = false;

    return () => {
      if (released) return;
      released = true;
      this.#watchers -= 1;
      if (this.#watchers === 0) this.#stop();
    };
  }

  /** Reads the overview again. A read already in flight is not doubled. */
  async refresh(): Promise<void> {
    if (this.#reading) return;
    this.#reading = true;
    try {
      this.#overview = await this.#api.getOverview();
      this.#overviewFault = undefined;
    } catch (thrown) {
      this.#overviewFault = thrown;
    } finally {
      this.#reading = false;
    }
  }

  async loadSettings(): Promise<void> {
    try {
      this.#settings = await this.#api.getSettings();
      this.#settingsFault = undefined;
    } catch (thrown) {
      this.#settingsFault = thrown;
    }
  }

  /**
   * The whole settings object a pane sends: what is saved, with the pane's
   * own changes over it. The process replaces its settings whole, so a pane
   * that sent only its own fields would erase every other pane's.
   */
  settingsBody(changes: SettingsChanges): Partial<PlugSettings> {
    const saved = this.#settings?.settings;
    const password = changes.mqtt_password ?? '';
    const token = changes.ha_token ?? '';

    return {
      ...saved,
      ...changes,
      mqtt_password: password,
      keep_password: password === '' && Boolean(saved?.has_password),
      ha_token: token,
      keep_ha_token: token === '' && Boolean(saved?.has_ha_token),
    };
  }

  /** Saves a pane's changes. It throws what the process refused. */
  async saveSettings(changes: SettingsChanges): Promise<SettingsResponse> {
    if (this.#settings === undefined) {
      this.#settings = await this.#api.getSettings();
    }
    const saved = await this.#api.saveSettings(this.settingsBody(changes));
    this.#settings = saved;
    this.#settingsFault = undefined;
    void this.refresh();

    return saved;
  }

  async loadAssignments(): Promise<void> {
    try {
      const read = await this.#api.getAssignments();
      this.#assignments = read;
      this.#assignmentsFault = undefined;
      const entities = read.ha_entities ?? [];
      if (entities.length > 0) this.#haEntities = entities;
    } catch (thrown) {
      this.#assignmentsFault = thrown;
    }
  }

  /** Asks the broker which plugs are there, then reads the list again. */
  async scan(): Promise<void> {
    await this.#api.discoverPlugs();
    await this.loadAssignments();
  }

  /** Keeps the entities a connection test answered, for the entity pickers. */
  keepHaEntities(entities: readonly HAEntity[]): void {
    this.#haEntities = entities;
  }

  /** Saves one printer's plug. It throws what the process refused. */
  async saveConfig(
    deviceId: string,
    config: DeviceConfig,
  ): Promise<PowerStatus> {
    const status = await this.#api.saveConfig(deviceId, config);
    await Promise.all([this.refresh(), this.loadAssignments()]);

    return status;
  }

  async setAutoOff(deviceId: string, enabled: boolean): Promise<PowerStatus> {
    const status = await this.#api.setAutoOff(deviceId, enabled);
    await this.refresh();

    return status;
  }

  /** Switches one printer's plug. It throws what the process refused. */
  async sendPower(
    deviceId: string,
    action: 'on' | 'off',
  ): Promise<PowerStatus> {
    const status = await this.#api.sendPower(deviceId, action);
    await this.refresh();

    return status;
  }

  #stop(): void {
    if (this.#timer !== undefined) clearInterval(this.#timer);
    this.#timer = undefined;
  }
}

const shared = new WeakMap<ModuleHost, PowerStore>();

/** The one store this host's screens share. */
export const powerFor = (host: ModuleHost): PowerStore => {
  let store = shared.get(host);
  if (store === undefined) {
    // Built under its own root: the first screen to ask is not the owner, and
    // a derived owned by a screen goes inert when that screen is destroyed.
    let created!: PowerStore;
    const dispose = $effect.root(() => {
      created = new PowerStore(createApi(host), host.signal);
    });
    host.signal.addEventListener('abort', dispose, { once: true });
    store = created;
    shared.set(host, store);
  }

  return store;
};
