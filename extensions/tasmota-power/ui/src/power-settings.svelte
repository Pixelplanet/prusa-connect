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

  let settingsMeta = $state<SettingsResponse | undefined>(undefined);
  let assignmentsData = $state<AssignmentsResponse | undefined>(undefined);
  let manualOpen = $state<Record<string, boolean>>({});
  let manualTopics = $state<Record<string, string>>({});

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
      mqtt_port: Number.isFinite(parsedPort) && parsedPort > 0 ? parsedPort : mqttTls ? 8883 : 1883,
      mqtt_tls: mqttTls,
      mqtt_username: mqttUsername.trim(),
      mqtt_password: mqttPassword,
      keep_password: mqttPassword === '' && hasSavedPassword,
      mqtt_client_id: mqttClientId.trim() || 'prusa-connect-tasmota',
      auto_off_cooldown_sec: Number.isFinite(parsedCooldown) && parsedCooldown >= 0 ? parsedCooldown : 120,
    };
  }

  function applySettingsResponse(res: SettingsResponse) {
    settingsMeta = res;
    mqttHost = res.settings.mqtt_host;
    mqttPort = String(res.settings.mqtt_port || (res.settings.mqtt_tls ? 8883 : 1883));
    mqttTls = res.settings.mqtt_tls;
    mqttUsername = res.settings.mqtt_username;
    mqttPassword = '';
    hasSavedPassword = res.settings.has_password;
    mqttClientId = res.settings.mqtt_client_id || 'prusa-connect-tasmota';
    cooldownSec = String(res.settings.auto_off_cooldown_sec ?? 120);
  }

  async function refreshAll() {
    try {
      const [sRes, aRes] = await Promise.all([
        api.getSettings(),
        api.getAssignments(),
      ]);
      applySettingsResponse(sRes);
      assignmentsData = aRes;
      const nextManual: Record<string, string> = {};
      for (const dev of devices) {
        nextManual[dev.id] = aRes.assignments[dev.id]?.tasmota_topic ?? '';
      }
      manualTopics = nextManual;
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
      assignmentsData = await api.getAssignments();
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
      assignmentsData = await api.getAssignments();
    } catch (err) {
      assignmentError = err instanceof Error ? err.message : String(err);
    } finally {
      scanning = false;
    }
  }

  function ownerOfTopic(topic: string, excludeDeviceId: string): string | undefined {
    if (!topic || !assignmentsData) return undefined;
    for (const [devId, cfg] of Object.entries(assignmentsData.assignments)) {
      if (devId !== excludeDeviceId && cfg.tasmota_topic === topic) {
        const dev = fleet.device(devId);
        return dev?.name || devId;
      }
    }
    return undefined;
  }

  function plugLabel(plug: PlugTelemetry, currentDeviceId: string): string {
    const topic = plug.topic ?? '';
    const base = plug.friendly_name ? `${plug.friendly_name} (${topic})` : topic;
    const watts =
      plug.power === 'ON' && plug.power_watts !== undefined
        ? ` · ${Math.round(plug.power_watts)} W`
        : '';
    const owner = ownerOfTopic(topic, currentDeviceId);
    const inUse = owner
      ? ` ${t('tasmota-power.settings.inUseBy', { printer: owner })}`
      : '';
    return `${base} · ${plug.power}${watts}${inUse}`;
  }

  async function assignTopic(deviceId: string, topic: string) {
    savingDeviceId = deviceId;
    assignmentError = '';
    try {
      const existing = assignmentsData?.assignments[deviceId];
      await api.saveConfig(deviceId, {
        tasmota_topic: topic.trim(),
        auto_off_after_print: existing?.auto_off_after_print ?? false,
        cooldown_sec: existing?.cooldown_sec,
      });
      assignmentsData = await api.getAssignments();
      manualOpen[deviceId] = false;
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
      message={settingsMessage || undefined}
      messageIsFailure={settingsMessageIsError}
      edged
    >
      <div class="flex flex-col gap-4">
        <!-- Live Status Row -->
        <div class="flex flex-wrap items-center justify-between gap-2 rounded-sm border border-border bg-background/50 px-3 py-2 text-xs">
          <div class="flex items-center gap-2">
            {#if settingsMeta?.mqtt_connected}
              <Badge variant="default">
                {t('tasmota-power.settings.connected')}
              </Badge>
            {:else}
              <Badge variant="destructive">
                {t('tasmota-power.settings.disconnected')}
              </Badge>
            {/if}
            <span class="text-muted-foreground font-mono">
              {settingsMeta?.mqtt_broker || t('tasmota-power.settings.notConfigured')}
            </span>
          </div>
          {#if settingsMeta?.mqtt_error && !settingsMeta.mqtt_connected}
            <span class="text-destructive text-xs">{settingsMeta.mqtt_error}</span>
          {/if}
        </div>

        <!-- Row 1: Host | Port | TLS -->
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-12 sm:items-end">
          <div class="sm:col-span-7 flex flex-col gap-1.5">
            <Label for="mqtt-host">{t('tasmota-power.settings.hostLabel')}</Label>
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
            <Label for="mqtt-port">{t('tasmota-power.settings.portLabel')}</Label>
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
            <Label for="mqtt-user">{t('tasmota-power.settings.usernameLabel')}</Label>
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
            <Label for="mqtt-pass">{t('tasmota-power.settings.passwordLabel')}</Label>
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

        <!-- Row 3: Cooldown & Client ID inline -->
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
          </div>
        </div>
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
        <div class="divide-y divide-border rounded-sm border border-border">
          {#each devices as dev (dev.id)}
            {@const currentTopic =
              assignmentsData?.assignments[dev.id]?.tasmota_topic ?? ''}
            {@const busy = savingDeviceId === dev.id}
            {@const hasDiscovered =
              (assignmentsData?.discovered_plugs?.length ?? 0) > 0}
            {@const showManual = manualOpen[dev.id] || !hasDiscovered}
            <div class="flex flex-col gap-2 p-3 sm:flex-row sm:items-center sm:justify-between">
              <div class="min-w-0">
                <div class="text-sm font-medium truncate">
                  {dev.name || dev.id}
                </div>
                <div class="text-xs text-muted-foreground">
                  {dev.model ?? 'Printer'}
                  {#if currentTopic}
                    · <code class="font-mono">{currentTopic}</code>
                  {/if}
                </div>
              </div>

              <div class="flex flex-wrap items-center gap-2">
                {#if hasDiscovered && !manualOpen[dev.id]}
                  <select
                    class="h-8 rounded-sm border border-input bg-background px-2.5 text-xs min-w-[230px]"
                    value={currentTopic}
                    disabled={busy}
                    onchange={(e) =>
                      void assignTopic(
                        dev.id,
                        (e.currentTarget as HTMLSelectElement).value,
                      )}
                  >
                    <option value="">
                      {t('tasmota-power.settings.unassigned')}
                    </option>
                    {#each assignmentsData?.discovered_plugs ?? [] as plug (plug.topic)}
                      {@const topic = plug.topic ?? ''}
                      {@const usedByOther =
                        ownerOfTopic(topic, dev.id) !== undefined}
                      <option value={topic} disabled={usedByOther}>
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
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={busy}
                    onclick={() =>
                      void assignTopic(dev.id, manualTopics[dev.id] ?? '')}
                  >
                    {t('tasmota-power.settings.saveTopic')}
                  </Button>
                {/if}

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
                    variant="ghost"
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
