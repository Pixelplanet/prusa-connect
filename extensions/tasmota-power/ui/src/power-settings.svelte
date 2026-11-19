<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Badge,
    Button,
    Input,
    Label,
    SettingsPane,
    Switch,
  } from '@prusa/connect-ui/primitives';

  import {
    createApi,
    type AssignmentsResponse,
    type BrokerTestResult,
    type PlugTelemetry,
    type SettingsResponse,
  } from './api';
  import { heldHost } from './host';

  const host = heldHost();
  const api = createApi(host);
  const fleet = host.fleet;
  const t = host.t;

  // Broker form state
  let mqttHost = $state('');
  let mqttPort = $state('1883');
  let mqttTls = $state(false);
  let mqttUsername = $state('');
  let mqttPassword = $state('');
  let hasSavedPassword = $state(false);
  let mqttClientId = $state('prusa-connect-tasmota');
  let cooldownSec = $state('120');
  let confirmTurnOff = $state(true);

  let settingsMeta = $state<SettingsResponse | undefined>(undefined);
  let assignmentsData = $state<AssignmentsResponse | undefined>(undefined);
  let manualOpen = $state<Record<string, boolean>>({});
  let selectedTopics = $state<Record<string, string>>({});
  let selectedRelays = $state<Record<string, number>>({});
  let manualTopics = $state<Record<string, string>>({});
  let justSaved = $state<Record<string, boolean>>({});

  let savingSettings = $state(false);
  let testing = $state(false);
  let scanning = $state(false);
  let savingDeviceId = $state('');

  let settingsMessage = $state('');
  let settingsMessageIsError = $state(false);
  let testResult = $state<BrokerTestResult | undefined>(undefined);
  let assignmentError = $state('');

  const devices = $derived(fleet.devices);

  function buildSettingsPayload() {
    const parsedPort = Number.parseInt(mqttPort, 10);
    const parsedCooldown = Number.parseInt(cooldownSec, 10);
    return {
      mqtt_host: mqttHost.trim(),
      mqtt_port:
        Number.isFinite(parsedPort) && parsedPort > 0
          ? parsedPort
          : mqttTls
            ? 8883
            : 1883,
      mqtt_tls: mqttTls,
      mqtt_username: mqttUsername.trim(),
      mqtt_password: mqttPassword,
      keep_password: mqttPassword === '' && hasSavedPassword,
      mqtt_client_id: mqttClientId.trim() || 'prusa-connect-tasmota',
      auto_off_cooldown_sec:
        Number.isFinite(parsedCooldown) && parsedCooldown >= 0
          ? parsedCooldown
          : 120,
      confirm_turn_off: confirmTurnOff,
    };
  }

  function applySettingsResponse(res: SettingsResponse) {
    settingsMeta = res;
    mqttHost = res.settings.mqtt_host;
    mqttPort = String(
      res.settings.mqtt_port || (res.settings.mqtt_tls ? 8883 : 1883),
    );
    mqttTls = res.settings.mqtt_tls;
    mqttUsername = res.settings.mqtt_username;
    mqttPassword = '';
    hasSavedPassword = res.settings.has_password;
    mqttClientId = res.settings.mqtt_client_id || 'prusa-connect-tasmota';
    cooldownSec = String(res.settings.auto_off_cooldown_sec ?? 120);
    confirmTurnOff = res.settings.confirm_turn_off ?? true;
  }

  function syncAssignmentDrafts(aRes: AssignmentsResponse) {
    assignmentsData = aRes;
    const nextSelected: Record<string, string> = {};
    const nextRelays: Record<string, number> = {};
    const nextManual: Record<string, string> = {};
    for (const dev of devices) {
      const cfg = aRes.assignments[dev.id];
      const topic = cfg?.tasmota_topic ?? '';
      nextSelected[dev.id] = topic;
      nextRelays[dev.id] = cfg?.relay_index || 1;
      nextManual[dev.id] = topic;
    }
    selectedTopics = nextSelected;
    selectedRelays = nextRelays;
    manualTopics = nextManual;
  }

  async function refreshAll() {
    try {
      const [sRes, aRes] = await Promise.all([
        api.getSettings(),
        api.getAssignments(),
      ]);
      applySettingsResponse(sRes);
      syncAssignmentDrafts(aRes);
    } catch (err) {
      settingsMessage = err instanceof Error ? err.message : String(err);
      settingsMessageIsError = true;
    }
  }

  onMount(() => {
    void refreshAll();
  });

  function handleTlsToggle(next: boolean) {
    mqttTls = next;
    if (next && mqttPort === '1883') mqttPort = '8883';
    if (!next && mqttPort === '8883') mqttPort = '1883';
  }

  async function handleTest() {
    testing = true;
    testResult = undefined;
    settingsMessage = '';
    try {
      const res = await api.testBroker(buildSettingsPayload());
      testResult = res;
      if (res.ok) {
        settingsMessage = t('tasmota-power.settings.testOk', {
          broker: res.broker ?? '',
        });
        settingsMessageIsError = false;
      } else {
        settingsMessage = t('tasmota-power.settings.testFail', {
          error: res.error ?? 'unknown error',
        });
        settingsMessageIsError = true;
      }
    } catch (err) {
      settingsMessage = err instanceof Error ? err.message : String(err);
      settingsMessageIsError = true;
    } finally {
      testing = false;
    }
  }

  async function handleSaveSettings(event: SubmitEvent) {
    event.preventDefault();
    savingSettings = true;
    settingsMessage = '';
    testResult = undefined;
    try {
      const res = await api.saveSettings(buildSettingsPayload());
      applySettingsResponse(res);
      syncAssignmentDrafts(await api.getAssignments());
      if (res.mqtt_error) {
        settingsMessage = res.mqtt_error;
        settingsMessageIsError = true;
      } else {
        settingsMessage = t('tasmota-power.settings.savedMessage');
        settingsMessageIsError = false;
      }
    } catch (err) {
      settingsMessage = err instanceof Error ? err.message : String(err);
      settingsMessageIsError = true;
    } finally {
      savingSettings = false;
    }
  }

  async function handleScan() {
    scanning = true;
    assignmentError = '';
    try {
      await api.discoverPlugs();
      syncAssignmentDrafts(await api.getAssignments());
    } catch (err) {
      assignmentError = err instanceof Error ? err.message : String(err);
    } finally {
      scanning = false;
    }
  }

  function ownerOfTopic(
    topic: string,
    relayIndex: number,
    excludeDeviceId: string,
  ): string | undefined {
    if (!topic || !assignmentsData) return undefined;
    const targetRelay = relayIndex || 1;
    for (const [devId, cfg] of Object.entries(assignmentsData.assignments)) {
      if (
        devId !== excludeDeviceId &&
        cfg.tasmota_topic === topic &&
        (cfg.relay_index || 1) === targetRelay
      ) {
        const dev = fleet.device(devId);
        return dev?.name || devId;
      }
    }
    return undefined;
  }

  function plugByTopic(topic: string): PlugTelemetry | undefined {
    return assignmentsData?.discovered_plugs?.find((p) => p.topic === topic);
  }

  function plugHasFreeRelay(plug: PlugTelemetry, excludeDeviceId: string): boolean {
    if (!plug.topic) return true;
    const count = Math.max(1, plug.relay_count ?? 1);
    for (let r = 1; r <= count; r++) {
      if (!ownerOfTopic(plug.topic, r, excludeDeviceId)) {
        return true;
      }
    }
    return false;
  }

  function firstFreeRelay(
    plug: PlugTelemetry | undefined,
    excludeDeviceId: string,
  ): number {
    if (!plug?.topic) return 1;
    const count = Math.max(1, plug.relay_count ?? 1);
    for (let r = 1; r <= count; r++) {
      if (!ownerOfTopic(plug.topic, r, excludeDeviceId)) {
        return r;
      }
    }
    return 1;
  }

  function relayDisplayState(
    plug: PlugTelemetry | undefined,
    relayIndex: number,
  ): string {
    if (!plug) return 'UNKNOWN';
    if (plug.online === false) return 'Offline';
    return plug.relays?.[String(relayIndex)] ?? plug.power;
  }

  function plugLabel(plug: PlugTelemetry, currentDeviceId: string): string {
    const topic = plug.topic ?? '';
    const base = plug.friendly_name ? `${plug.friendly_name} (${topic})` : topic;
    const relaysCount = Math.max(1, plug.relay_count ?? 1);
    const stateText =
      plug.online === false
        ? 'Offline'
        : relaysCount > 1
          ? `${relaysCount} relays`
          : plug.power;
    const watts =
      plug.online !== false && plug.power === 'ON' && plug.power_watts !== undefined
        ? ` · ${Math.round(plug.power_watts)} W`
        : '';
    let inUse = '';
    if (!plugHasFreeRelay(plug, currentDeviceId)) {
      const owner = ownerOfTopic(topic, 1, currentDeviceId);
      if (owner) {
        inUse = ` ${t('tasmota-power.settings.inUseBy', { printer: owner })}`;
      }
    }
    return `${base} · ${stateText}${watts}${inUse}`;
  }

  async function assignTopic(deviceId: string, topic: string, relayIndex = 1) {
    savingDeviceId = deviceId;
    assignmentError = '';
    justSaved[deviceId] = false;
    try {
      const existing = assignmentsData?.assignments[deviceId];
      const cleanTopic = topic.trim();
      await api.saveConfig(deviceId, {
        tasmota_topic: cleanTopic,
        relay_index: cleanTopic ? relayIndex : undefined,
        auto_off_after_print: existing?.auto_off_after_print ?? false,
        cooldown_sec: existing?.cooldown_sec,
      });
      syncAssignmentDrafts(await api.getAssignments());
      manualOpen[deviceId] = false;
      justSaved[deviceId] = true;
      setTimeout(() => {
        justSaved[deviceId] = false;
      }, 3000);
    } catch (err) {
      assignmentError = err instanceof Error ? err.message : String(err);
    } finally {
      savingDeviceId = '';
    }
  }
</script>

<div class="flex flex-col gap-6">
  <!-- Pane 1: Unified MQTT Broker Configuration & Connection Test -->
  <form onsubmit={(e) => void handleSaveSettings(e)}>
    <SettingsPane
      title={t('tasmota-power.settings.mqttTitle')}
      description={t('tasmota-power.settings.mqttDescription')}
      edged
    >
      <div class="flex flex-col gap-4">
        <!-- Live Status Row -->
        <div class="flex flex-wrap items-center justify-between gap-2 rounded-sm border border-border bg-background/50 px-3 py-2 text-xs">
          <div class="flex items-center gap-2">
            {#if settingsMeta?.mqtt_connected}
              <Badge variant="success">
                {t('tasmota-power.settings.connected')}
              </Badge>
            {:else}
              <Badge variant="destructive">
                {t('tasmota-power.settings.disconnected')}
              </Badge>
            {/if}
            <span class="text-muted-foreground font-mono">
              {settingsMeta?.mqtt_broker ||
                t('tasmota-power.settings.notConfigured')}
            </span>
          </div>
          {#if settingsMeta?.mqtt_error && !settingsMeta.mqtt_connected}
            <span class="text-destructive text-xs">
              {settingsMeta.mqtt_error}
            </span>
          {/if}
        </div>

        <!-- Row 1: Host | Port | TLS -->
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-12 sm:items-end">
          <div class="sm:col-span-7 flex flex-col gap-1.5">
            <Label for="mqtt-host">
              {t('tasmota-power.settings.hostLabel')}
            </Label>
            <Input
              id="mqtt-host"
              type="text"
              placeholder={t('tasmota-power.settings.hostPlaceholder')}
              value={mqttHost}
              oninput={(e: Event) =>
                (mqttHost = (e.currentTarget as HTMLInputElement).value)}
            />
          </div>

          <div class="sm:col-span-3 flex flex-col gap-1.5">
            <Label for="mqtt-port">
              {t('tasmota-power.settings.portLabel')}
            </Label>
            <Input
              id="mqtt-port"
              type="number"
              min="1"
              max="65535"
              value={mqttPort}
              oninput={(e: Event) =>
                (mqttPort = (e.currentTarget as HTMLInputElement).value)}
            />
          </div>

          <div class="sm:col-span-2 flex items-center justify-between sm:justify-start gap-2 h-9">
            <Switch
              id="mqtt-tls"
              checked={mqttTls}
              onCheckedChange={handleTlsToggle}
            />
            <Label for="mqtt-tls" variant="option">
              {t('tasmota-power.settings.tlsLabel')}
            </Label>
          </div>
        </div>

        <!-- Row 2: Username & Password inline -->
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div class="flex flex-col gap-1.5">
            <Label for="mqtt-user">
              {t('tasmota-power.settings.usernameLabel')}
            </Label>
            <Input
              id="mqtt-user"
              type="text"
              autocomplete="username"
              placeholder={t('tasmota-power.settings.usernamePlaceholder')}
              value={mqttUsername}
              oninput={(e: Event) =>
                (mqttUsername = (e.currentTarget as HTMLInputElement).value)}
            />
          </div>

          <div class="flex flex-col gap-1.5">
            <Label for="mqtt-pass">
              {t('tasmota-power.settings.passwordLabel')}
            </Label>
            <Input
              id="mqtt-pass"
              type="password"
              autocomplete="current-password"
              placeholder={hasSavedPassword
                ? t('tasmota-power.settings.passwordSaved')
                : t('tasmota-power.settings.passwordPlaceholder')}
              value={mqttPassword}
              oninput={(e: Event) =>
                (mqttPassword = (e.currentTarget as HTMLInputElement).value)}
            />
          </div>
        </div>

        <!-- Row 3: Cooldown & Client ID inline with explanations -->
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div class="flex flex-col gap-1.5">
            <Label for="mqtt-cooldown">
              {t('tasmota-power.settings.cooldownLabel')}
            </Label>
            <Input
              id="mqtt-cooldown"
              type="number"
              min="0"
              value={cooldownSec}
              oninput={(e: Event) =>
                (cooldownSec = (e.currentTarget as HTMLInputElement).value)}
            />
            <p class="text-xs text-muted-foreground leading-relaxed">
              {t('tasmota-power.settings.cooldownHint')}
            </p>
          </div>

          <div class="flex flex-col gap-1.5">
            <Label for="mqtt-client-id">
              {t('tasmota-power.settings.clientIdLabel')}
            </Label>
            <Input
              id="mqtt-client-id"
              type="text"
              value={mqttClientId}
              oninput={(e: Event) =>
                (mqttClientId = (e.currentTarget as HTMLInputElement).value)}
            />
            <p class="text-xs text-muted-foreground leading-relaxed">
              {t('tasmota-power.settings.clientIdHint')}
            </p>
          </div>
        </div>

        <!-- Row 4: Turn-off confirmation toggle -->
        <div class="flex items-start justify-between gap-4 rounded-sm border border-border bg-background/40 px-3 py-2.5">
          <div class="flex flex-col gap-0.5">
            <Label for="confirm-turn-off" variant="option">
              {t('tasmota-power.settings.confirmTurnOffLabel')}
            </Label>
            <p class="text-xs text-muted-foreground leading-relaxed">
              {t('tasmota-power.settings.confirmTurnOffHint')}
            </p>
          </div>
          <Switch
            id="confirm-turn-off"
            checked={confirmTurnOff}
            onCheckedChange={(v: boolean) => (confirmTurnOff = v)}
          />
        </div>

        <!-- Green / Red Test or Save Feedback Banner -->
        {#if settingsMessage}
          <div
            role={settingsMessageIsError ? 'alert' : 'status'}
            class={[
              'rounded-sm border px-3 py-2 text-xs font-medium',
              settingsMessageIsError
                ? 'border-destructive bg-destructive/15 text-destructive-foreground'
                : 'border-success bg-success/15 text-emerald-400',
            ]}
          >
            {settingsMessageIsError ? '✕ ' : '✓ '}
            {settingsMessage}
          </div>
        {/if}
      </div>

      {#snippet actions()}
        <Button
          type="button"
          variant="outline"
          disabled={testing || savingSettings || !mqttHost.trim()}
          onclick={() => void handleTest()}
        >
          {testing
            ? t('tasmota-power.settings.testing')
            : t('tasmota-power.settings.testButton')}
        </Button>
        <Button
          type="submit"
          variant="default"
          disabled={savingSettings || testing}
        >
          {savingSettings
            ? t('tasmota-power.settings.saving')
            : t('tasmota-power.settings.saveButton')}
        </Button>
      {/snippet}
    </SettingsPane>
  </form>

  <!-- Pane 2: Printer <-> Tasmota Plug Assignments -->
  <SettingsPane
    title={t('tasmota-power.settings.assignmentsTitle')}
    description={t('tasmota-power.settings.assignmentsDescription')}
    message={assignmentError || undefined}
    messageIsFailure={assignmentError !== ''}
    edged
  >
    <div class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <span class="text-xs text-muted-foreground">
          {t('tasmota-power.settings.discoveredCount', {
            count: assignmentsData?.discovered_plugs?.length ?? 0,
          })}
        </span>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={scanning || !settingsMeta?.mqtt_connected}
          onclick={() => void handleScan()}
        >
          {scanning
            ? t('tasmota-power.settings.scanning')
            : t('tasmota-power.settings.scanButton')}
        </Button>
      </div>

      {#if devices.length === 0}
        <p class="text-sm text-muted-foreground py-2">
          {t('tasmota-power.settings.noPrinters')}
        </p>
      {:else}
        <div class="flex flex-col gap-3">
          {#each devices as dev (dev.id)}
            {@const currentCfg = assignmentsData?.assignments[dev.id]}
            {@const currentTopic = currentCfg?.tasmota_topic ?? ''}
            {@const currentRelay = currentCfg?.relay_index || 1}
            {@const draftTopic = selectedTopics[dev.id] ?? currentTopic}
            {@const draftRelay = selectedRelays[dev.id] || 1}
            {@const assignedPlug = currentTopic
              ? plugByTopic(currentTopic)
              : undefined}
            {@const hasDiscovered =
              (assignmentsData?.discovered_plugs?.length ?? 0) > 0}
            {@const showManual = manualOpen[dev.id] || !hasDiscovered}
            {@const draftPlug = showManual
              ? plugByTopic((manualTopics[dev.id] ?? '').trim())
              : plugByTopic(draftTopic)}
            {@const relayCount = Math.max(
              showManual ? 4 : 1,
              draftPlug?.relay_count ?? 1,
              draftRelay,
            )}
            {@const showRelaySelect =
              (showManual && (manualTopics[dev.id] ?? '').trim() !== '') ||
              (draftTopic !== '' && (draftPlug?.relay_count ?? 1) > 1) ||
              currentRelay > 1}
            {@const busy = savingDeviceId === dev.id}
            {@const isDirty = showManual
              ? (manualTopics[dev.id] ?? currentTopic).trim() !== currentTopic ||
                (currentTopic !== '' && draftRelay !== currentRelay)
              : draftTopic !== currentTopic ||
                (draftTopic !== '' && draftRelay !== currentRelay)}

            <div class="flex flex-col gap-3 rounded-sm border border-border bg-background/40 p-3 sm:flex-row sm:items-center sm:justify-between">
              <div class="flex flex-col gap-1 min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="text-sm font-semibold truncate">
                    {dev.name || dev.id}
                  </span>
                  <Badge variant="outline">{dev.model ?? 'Printer'}</Badge>
                  {#if currentTopic}
                    {@const curRelayState = relayDisplayState(
                      assignedPlug,
                      currentRelay,
                    )}
                    <Badge
                      variant={assignedPlug?.online === false
                        ? 'destructive'
                        : curRelayState === 'ON'
                          ? 'success'
                          : 'secondary'}
                    >
                      {assignedPlug?.friendly_name
                        ? `${assignedPlug.friendly_name} (${currentTopic})`
                        : currentTopic}
                      {#if currentRelay > 1 || (assignedPlug?.relay_count ?? 1) > 1}
                        · {t('tasmota-power.section.relayBadge', {
                          relay: currentRelay,
                        })}
                      {/if}
                      {#if assignedPlug}
                        · {curRelayState}
                        {#if assignedPlug.online !== false && curRelayState === 'ON' && assignedPlug.power_watts !== undefined}
                          · {Math.round(assignedPlug.power_watts)} W
                        {/if}
                      {/if}
                    </Badge>
                  {:else}
                    <Badge variant="outline">
                      {t('tasmota-power.settings.noPlugAssigned')}
                    </Badge>
                  {/if}
                  {#if justSaved[dev.id]}
                    <Badge variant="success">
                      ✓ {t('tasmota-power.settings.assignedSaved')}
                    </Badge>
                  {/if}
                </div>
              </div>

              <div class="flex flex-wrap items-center gap-2">
                {#if hasDiscovered && !manualOpen[dev.id]}
                  <select
                    class="h-8 rounded-sm border border-input bg-background px-2.5 text-xs min-w-[230px]"
                    value={draftTopic}
                    disabled={busy}
                    onchange={(e) => {
                      const nextTopic = (
                        e.currentTarget as HTMLSelectElement
                      ).value;
                      selectedTopics[dev.id] = nextTopic;
                      if (nextTopic === currentTopic) {
                        selectedRelays[dev.id] = currentRelay;
                      } else {
                        selectedRelays[dev.id] = firstFreeRelay(
                          plugByTopic(nextTopic),
                          dev.id,
                        );
                      }
                    }}
                  >
                    <option value="">
                      {t('tasmota-power.settings.unassigned')}
                    </option>
                    {#each assignmentsData?.discovered_plugs ?? [] as plug (plug.topic)}
                      {@const topic = plug.topic ?? ''}
                      {@const fullyUsed = !plugHasFreeRelay(plug, dev.id)}
                      <option value={topic} disabled={fullyUsed}>
                        {plugLabel(plug, dev.id)}
                      </option>
                    {/each}
                  </select>
                {/if}

                {#if showManual}
                  <Input
                    type="text"
                    size="sm"
                    class="w-44"
                    placeholder={t('tasmota-power.settings.manualPlaceholder')}
                    value={manualTopics[dev.id] ?? currentTopic}
                    oninput={(e: Event) => {
                      manualTopics[dev.id] = (
                        e.currentTarget as HTMLInputElement
                      ).value;
                    }}
                  />
                {/if}

                {#if showRelaySelect}
                  {@const activeTopic = showManual
                    ? (manualTopics[dev.id] ?? '').trim()
                    : draftTopic}
                  <select
                    class="h-8 rounded-sm border border-input bg-background px-2 text-xs"
                    value={String(draftRelay)}
                    disabled={busy}
                    onchange={(e) => {
                      selectedRelays[dev.id] = Number.parseInt(
                        (e.currentTarget as HTMLSelectElement).value,
                        10,
                      );
                    }}
                  >
                    {#each Array.from({ length: relayCount }, (_, i) => i + 1) as rNum (rNum)}
                      {@const rOwner = ownerOfTopic(activeTopic, rNum, dev.id)}
                      {@const rName = draftPlug?.relay_names?.[rNum - 1]}
                      <option value={String(rNum)} disabled={Boolean(rOwner)}>
                        {t('tasmota-power.settings.relayOption', { relay: rNum })}
                        {rName && rName !== draftPlug?.friendly_name ? ` (${rName})` : ''}
                        {rOwner
                          ? ` ${t('tasmota-power.settings.inUseBy', { printer: rOwner })}`
                          : ''}
                      </option>
                    {/each}
                  </select>
                {/if}

                <Button
                  type="button"
                  size="sm"
                  variant={isDirty ? 'default' : 'outline'}
                  disabled={busy || !isDirty}
                  onclick={() =>
                    void assignTopic(
                      dev.id,
                      showManual
                        ? (manualTopics[dev.id] ?? '')
                        : draftTopic,
                      draftRelay,
                    )}
                >
                  {t('tasmota-power.settings.saveTopic')}
                </Button>

                {#if hasDiscovered}
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onclick={() => {
                      manualOpen[dev.id] = !manualOpen[dev.id];
                    }}
                  >
                    {t('tasmota-power.settings.manualToggle')}
                  </Button>
                {/if}

                {#if currentTopic}
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={busy}
                    onclick={() => void assignTopic(dev.id, '')}
                  >
                    {t('tasmota-power.settings.unassign')}
                  </Button>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  </SettingsPane>
</div>
