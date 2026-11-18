<script lang="ts">
  import { onMount } from 'svelte';
  import { Badge, Button } from '@prusa/connect-ui/primitives';

  import {
    createApi,
    type AssignmentsResponse,
    type BrokerTestResult,
    type PlugTelemetry,
  } from './api';
  import { heldHost } from './host';

  const host = heldHost();
  const api = createApi(host);
  const fleet = host.fleet;
  const t = host.t;

  let data = $state<AssignmentsResponse | undefined>(undefined);
  let manualTopics = $state<Record<string, string>>({});
  let testing = $state(false);
  let scanning = $state(false);
  let savingId = $state('');
  let testResult = $state<BrokerTestResult | undefined>(undefined);
  let errorMsg = $state('');

  const devices = $derived(fleet.devices);

  function ownerOfTopic(topic: string, excludeDeviceId: string): string | undefined {
    if (!topic || !data) return undefined;
    for (const [devId, cfg] of Object.entries(data.assignments)) {
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

  async function refresh() {
    try {
      const next = await api.getAssignments();
      data = next;
      const nextManual: Record<string, string> = {};
      for (const dev of devices) {
        nextManual[dev.id] = next.assignments[dev.id]?.tasmota_topic ?? '';
      }
      manualTopics = nextManual;
      errorMsg = '';
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    }
  }

  onMount(() => {
    void refresh();
  });

  async function handleTest() {
    testing = true;
    testResult = undefined;
    try {
      testResult = await api.testBroker();
      await refresh();
    } catch (err) {
      testResult = {
        ok: false,
        error: err instanceof Error ? err.message : String(err),
      };
    } finally {
      testing = false;
    }
  }

  async function handleScan() {
    scanning = true;
    errorMsg = '';
    try {
      await api.discoverPlugs();
      await refresh();
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      scanning = false;
    }
  }

  async function assignTopic(deviceId: string, topic: string) {
    savingId = deviceId;
    errorMsg = '';
    try {
      const existing = data?.assignments[deviceId];
      await api.saveConfig(deviceId, {
        tasmota_topic: topic.trim(),
        auto_off_after_print: existing?.auto_off_after_print ?? false,
        cooldown_sec: existing?.cooldown_sec,
      });
      await refresh();
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      savingId = '';
    }
  }
</script>

<div class="space-y-4">
  <!-- MQTT Broker Status & Test -->
  <div class="rounded-lg border p-4 space-y-3">
    <div class="flex items-center justify-between gap-2">
      <h3 class="text-sm font-semibold">{t('tasmota-power.settings.title')}</h3>
      {#if data}
        {#if data.mqtt_connected}
          <Badge variant="default">{t('tasmota-power.settings.connected')}</Badge>
        {:else}
          <Badge variant="destructive">
            {t('tasmota-power.settings.disconnected')}
          </Badge>
        {/if}
      {/if}
    </div>

    {#if data && !data.mqtt_configured}
      <p class="text-xs text-muted-foreground">
        {t('tasmota-power.settings.notConfigured')}
      </p>
    {:else if data?.mqtt_broker}
      <p class="text-xs text-muted-foreground">
        {t('tasmota-power.settings.broker', { broker: data.mqtt_broker })}
      </p>
    {/if}

    {#if data?.mqtt_error}
      <p class="text-xs text-destructive">{data.mqtt_error}</p>
    {/if}

    <div class="flex flex-wrap items-center gap-2">
      <Button
        size="sm"
        variant="outline"
        disabled={testing || !data?.mqtt_configured}
        onclick={() => void handleTest()}
      >
        {testing
          ? t('tasmota-power.settings.testing')
          : t('tasmota-power.settings.testButton')}
      </Button>
      {#if data?.mqtt_connected}
        <Button
          size="sm"
          variant="outline"
          disabled={scanning}
          onclick={() => void handleScan()}
        >
          {scanning
            ? t('tasmota-power.settings.scanning')
            : t('tasmota-power.settings.scanButton')}
        </Button>
      {/if}
    </div>

    {#if testResult}
      {#if testResult.ok}
        <p class="text-xs text-green-600">{t('tasmota-power.settings.testOk')}</p>
      {:else}
        <p class="text-xs text-destructive">
          {t('tasmota-power.settings.testFail', {
            error: testResult.error ?? 'unknown error',
          })}
        </p>
      {/if}
    {/if}
  </div>

  <!-- Printer <-> Plug Assignments -->
  <div class="rounded-lg border p-4 space-y-3">
    <div>
      <h3 class="text-sm font-semibold">
        {t('tasmota-power.settings.assignmentsTitle')}
      </h3>
      <p class="text-xs text-muted-foreground mt-0.5">
        {t('tasmota-power.settings.assignmentsHint')}
      </p>
    </div>

    {#if devices.length === 0}
      <p class="text-xs text-muted-foreground">
        {t('tasmota-power.settings.noPrinters')}
      </p>
    {:else}
      <div class="divide-y">
        {#each devices as dev (dev.id)}
          {@const currentTopic = data?.assignments[dev.id]?.tasmota_topic ?? ''}
          {@const busy = savingId === dev.id}
          <div class="py-3 first:pt-1 last:pb-1 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div class="min-w-0">
              <div class="text-sm font-medium truncate">
                {dev.name || dev.id}
              </div>
              <div class="text-xs text-muted-foreground">
                {dev.model ?? 'Printer'}
                {#if currentTopic}
                  · <code class="text-xs">{currentTopic}</code>
                {/if}
              </div>
            </div>

            <div class="flex flex-wrap items-center gap-2">
              {#if (data?.discovered_plugs?.length ?? 0) > 0}
                <select
                  class="rounded border bg-background px-2 py-1 text-xs min-w-[220px]"
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
                  {#each data?.discovered_plugs ?? [] as plug (plug.topic)}
                    {@const topic = plug.topic ?? ''}
                    {@const usedByOther =
                      ownerOfTopic(topic, dev.id) !== undefined}
                    <option value={topic} disabled={usedByOther}>
                      {plugLabel(plug, dev.id)}
                    </option>
                  {/each}
                </select>
              {:else}
                <input
                  type="text"
                  class="rounded border bg-background px-2 py-1 text-xs w-44"
                  placeholder={t('tasmota-power.settings.manualTopic')}
                  value={manualTopics[dev.id] ?? currentTopic}
                  oninput={(e) => {
                    manualTopics[dev.id] = (
                      e.currentTarget as HTMLInputElement
                    ).value;
                  }}
                />
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy}
                  onclick={() =>
                    void assignTopic(dev.id, manualTopics[dev.id] ?? '')}
                >
                  {t('tasmota-power.settings.save')}
                </Button>
              {/if}

              {#if currentTopic}
                <Button
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

    {#if errorMsg}
      <p class="text-xs text-destructive">{errorMsg}</p>
    {/if}
  </div>
</div>
