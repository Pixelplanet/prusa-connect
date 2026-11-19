<!--
  The MQTT broker the plugs talk through: one a person already runs, or the
  one built into this extension. The pane tests and saves its own fields and
  leaves every other setting as it is saved.

  The parent draws it once the settings are read, so the form starts from
  what is saved.
-->
<script lang="ts">
  import {
    Alert,
    AlertDescription,
    AlertTitle,
    Badge,
    Button,
    Collapsible,
    CollapsibleContent,
    CollapsibleTrigger,
    Field,
    Input,
    Label,
    RadioGroup,
    RadioGroupItem,
    SettingsPane,
    Switch,
  } from '@prusa/connect-ui/primitives';

  import type { BrokerTestResult, SettingsResponse } from '../api';
  import FaultNote from '../fault-note.svelte';
  import { heldHost } from '../host';
  import { typed, wholeOf } from '../input';
  import LinkButton from '../link-button.svelte';
  import { portsTabHref } from '../links';
  import { powerFor, type SettingsChanges } from '../power.svelte';

  let { saved }: { saved: SettingsResponse } = $props();

  const PORT = 1883;
  const TLS_PORT = 8883;

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);
  const uid = $props.id();

  // The form starts from what is saved, and takes it again after each save.
  // svelte-ignore state_referenced_locally
  const start = saved.settings;
  // Nothing saved yet: the built-in broker is offered first to a person who
  // approved its port, and the other one to a person who did not.
  const unset = !start.mqtt_builtin && start.mqtt_host === '';
  // svelte-ignore state_referenced_locally
  let builtin = $state(
    start.mqtt_builtin || (unset && saved.mqtt_builtin_granted === true),
  );
  let brokerHost = $state(start.mqtt_host);
  let port = $state(String(start.mqtt_port || PORT));
  let tls = $state(start.mqtt_tls);
  let username = $state(start.mqtt_username);
  let password = $state('');
  let clientId = $state(start.mqtt_client_id);

  let saving = $state(false);
  let testing = $state(false);
  let fault = $state.raw<unknown>(undefined);
  let tested = $state.raw<BrokerTestResult | undefined>(undefined);
  let justSaved = $state(false);

  /** The address a plug on the network reaches this server at. */
  const lanHost = (): string => window.location.hostname || '127.0.0.1';

  // No port is answered while none is granted, and the form then names the
  // one the manifest asks for.
  const builtinPort = $derived(
    saved.mqtt_builtin_port === undefined || saved.mqtt_builtin_port === 0
      ? PORT
      : saved.mqtt_builtin_port,
  );
  const granted = $derived(saved.mqtt_builtin_granted !== false);
  // The built-in broker counts this extension's own client among its
  // clients, and that one is not a plug.
  const plugsConnected = $derived(
    Math.max(0, saved.mqtt_builtin_clients - (saved.mqtt_connected ? 1 : 0)),
  );

  const changes = $derived.by((): SettingsChanges => {
    const useTls = builtin ? false : tls;

    return {
      mqtt_builtin: builtin,
      mqtt_host: builtin ? brokerHost.trim() || lanHost() : brokerHost.trim(),
      mqtt_port: builtin
        ? builtinPort
        : wholeOf(port, 0) || (useTls ? TLS_PORT : PORT),
      mqtt_tls: useTls,
      mqtt_username: username.trim(),
      mqtt_password: password,
      mqtt_client_id: clientId.trim(),
    };
  });

  const dirty = $derived.by(() => {
    const now = saved.settings;

    return (
      password !== '' ||
      changes.mqtt_builtin !== now.mqtt_builtin ||
      changes.mqtt_tls !== now.mqtt_tls ||
      changes.mqtt_username !== now.mqtt_username ||
      (changes.mqtt_client_id !== '' &&
        changes.mqtt_client_id !== now.mqtt_client_id) ||
      (!builtin &&
        (changes.mqtt_host !== now.mqtt_host ||
          changes.mqtt_port !== now.mqtt_port))
    );
  });
  const canTest = $derived(builtin || brokerHost.trim() !== '');

  const pickMode = (value: string): void => {
    builtin = value === 'builtin';
    tested = undefined;
    justSaved = false;
  };

  const setTls = (next: boolean): void => {
    tls = next;
    // The port follows the choice while it is still the other default.
    if (next && port === String(PORT)) port = String(TLS_PORT);
    if (!next && port === String(TLS_PORT)) port = String(PORT);
  };

  const test = async (): Promise<void> => {
    testing = true;
    tested = undefined;
    fault = undefined;
    justSaved = false;
    try {
      tested = await power.api.testBroker(power.settingsBody(changes));
    } catch (thrown) {
      fault = thrown;
    } finally {
      testing = false;
    }
  };

  const save = async (event: SubmitEvent): Promise<void> => {
    event.preventDefault();
    saving = true;
    tested = undefined;
    fault = undefined;
    justSaved = false;
    try {
      const answer = await power.saveSettings(changes);
      brokerHost = answer.settings.mqtt_host;
      port = String(answer.settings.mqtt_port || PORT);
      password = '';
      justSaved = true;
    } catch (thrown) {
      fault = thrown;
    } finally {
      saving = false;
    }
  };
</script>

<form onsubmit={(event) => void save(event)} data-part="mqtt-pane">
  <SettingsPane
    title={t('power.connections.mqtt.title')}
    description={t('power.connections.mqtt.description')}
    edged
  >
    <div class="flex flex-col gap-4">
      <div class="flex flex-wrap items-center gap-2 text-xs" data-part="status">
        {#if saved.mqtt_connected}
          <Badge variant="success">{t('power.connections.connected')}</Badge>
        {:else if saved.mqtt_configured}
          <Badge variant="destructive">
            {t('power.connections.notConnected')}
          </Badge>
        {:else}
          <Badge variant="outline">{t('power.connections.notSetUp')}</Badge>
        {/if}
        {#if saved.mqtt_broker}
          <span class="text-muted-foreground font-mono">
            {saved.mqtt_broker}
          </span>
        {/if}
        {#if saved.mqtt_builtin_running}
          <span class="text-muted-foreground">
            {t('power.connections.mqtt.clients', { count: plugsConnected })}
          </span>
        {/if}
        <span class="text-muted-foreground">
          {t('power.connections.usedBy', { count: power.used.mqtt })}
        </span>
      </div>

      {#if saved.mqtt_error && !saved.mqtt_connected}
        <Alert variant="destructive" data-part="mqtt-error">
          <AlertTitle>{t('power.connections.mqtt.failed')}</AlertTitle>
          <AlertDescription>{saved.mqtt_error}</AlertDescription>
        </Alert>
      {/if}
      {#if saved.mqtt_warning}
        <Alert variant="warning" data-part="mqtt-warning">
          <AlertDescription>{saved.mqtt_warning}</AlertDescription>
        </Alert>
      {/if}

      <RadioGroup
        value={builtin ? 'builtin' : 'external'}
        aria-label={t('power.connections.mqtt.mode')}
        onValueChange={pickMode}
      >
        <div class="flex items-start gap-3 py-1">
          <RadioGroupItem
            value="external"
            id={`${uid}-external`}
            class="mt-0.5"
          />
          <div class="flex min-w-0 flex-col gap-0.5">
            <Label for={`${uid}-external`} variant="option">
              {t('power.connections.mqtt.external')}
            </Label>
            <p class="text-muted-foreground text-xs">
              {t('power.connections.mqtt.externalHint')}
            </p>
          </div>
        </div>
        <div class="flex items-start gap-3 py-1">
          <RadioGroupItem
            value="builtin"
            id={`${uid}-builtin`}
            class="mt-0.5"
            disabled={!granted && !builtin}
          />
          <div class="flex min-w-0 flex-col gap-0.5">
            <Label for={`${uid}-builtin`} variant="option">
              {t('power.connections.mqtt.builtin')}
            </Label>
            <p class="text-muted-foreground text-xs">
              {t('power.connections.mqtt.builtinHint')}
            </p>
            {#if !granted && !builtin}
              <p
                class="text-muted-foreground text-xs"
                data-part="builtin-locked"
              >
                {t('power.connections.mqtt.locked')}
                <LinkButton href={portsTabHref()}>
                  {t('power.connections.mqtt.openPorts')}
                </LinkButton>
              </p>
            {/if}
          </div>
        </div>
      </RadioGroup>

      {#if builtin}
        {#if granted}
          <Alert data-part="builtin-target">
            <AlertDescription>
              {t('power.connections.mqtt.builtinTarget', {
                host: brokerHost.trim() || lanHost(),
                port: String(builtinPort),
              })}
            </AlertDescription>
          </Alert>
        {:else}
          <Alert variant="warning" data-part="builtin-ungranted">
            <AlertTitle>{t('power.connections.mqtt.ungranted')}</AlertTitle>
            <AlertDescription>
              {t('power.connections.mqtt.ungrantedNote')}
              <LinkButton href={portsTabHref()}>
                {t('power.connections.mqtt.openPorts')}
              </LinkButton>
            </AlertDescription>
          </Alert>
        {/if}
      {:else}
        <div class="grid grid-cols-1 gap-4 @md/pane:grid-cols-3">
          <Field
            class="@md/pane:col-span-2"
            label={t('power.connections.mqtt.host')}
            description={saved.mqtt_host_pinned
              ? t('power.connections.pinned')
              : t('power.connections.mqtt.hostHint')}
            control={Input}
            controlProps={{
              value: brokerHost,
              oninput: typed((value) => (brokerHost = value)),
              placeholder: t('power.connections.mqtt.hostExample'),
              autocomplete: 'off',
              disabled: saved.mqtt_host_pinned === true,
            }}
          />
          <Field
            label={t('power.connections.mqtt.port')}
            control={Input}
            controlProps={{
              type: 'number',
              min: 1,
              max: 65535,
              value: port,
              oninput: typed((value) => (port = value)),
            }}
          />
        </div>
        <div class="flex items-center gap-2">
          <Switch id={`${uid}-tls`} checked={tls} onCheckedChange={setTls} />
          <Label for={`${uid}-tls`} variant="option">
            {t('power.connections.mqtt.tls')}
          </Label>
        </div>
      {/if}

      <div class="grid grid-cols-1 gap-4 @md/pane:grid-cols-2">
        <Field
          label={t('power.connections.mqtt.username')}
          control={Input}
          controlProps={{
            value: username,
            oninput: typed((value) => (username = value)),
            placeholder: t('power.field.optional'),
            autocomplete: 'off',
          }}
        />
        <Field
          label={t('power.connections.mqtt.password')}
          control={Input}
          controlProps={{
            type: 'password',
            value: password,
            oninput: typed((value) => (password = value)),
            placeholder: saved.settings.has_password
              ? t('power.field.secretSaved')
              : t('power.field.optional'),
            autocomplete: 'new-password',
          }}
        />
      </div>

      <Collapsible>
        <CollapsibleTrigger variant="inline">
          {t('power.connections.more')}
        </CollapsibleTrigger>
        <CollapsibleContent>
          <div class="pt-3">
            <Field
              label={t('power.connections.mqtt.clientId')}
              description={t('power.connections.mqtt.clientIdHint')}
              control={Input}
              controlProps={{
                value: clientId,
                oninput: typed((value) => (clientId = value)),
                autocomplete: 'off',
              }}
            />
          </div>
        </CollapsibleContent>
      </Collapsible>

      <FaultNote {fault} />
      {#if tested?.ok}
        <Alert variant="success" data-part="test-ok">
          <AlertDescription>
            {t('power.connections.mqtt.testOk', {
              broker: tested.broker ?? '',
            })}
          </AlertDescription>
        </Alert>
      {:else if tested}
        <Alert variant="destructive" data-part="test-failed">
          <AlertTitle>{t('power.connections.testFailed')}</AlertTitle>
          {#if tested.error}
            <AlertDescription>{tested.error}</AlertDescription>
          {/if}
        </Alert>
      {/if}
      {#if tested?.warning}
        <Alert variant="warning" data-part="test-warning">
          <AlertDescription>{tested.warning}</AlertDescription>
        </Alert>
      {/if}
      {#if justSaved}
        <Alert variant="success" data-part="saved">
          <AlertDescription>{t('power.connections.saved')}</AlertDescription>
        </Alert>
      {/if}
    </div>

    {#snippet actions()}
      <Button
        type="button"
        variant="secondary"
        disabled={!canTest || saving}
        pending={testing}
        onclick={() => void test()}
      >
        {t('power.connections.test')}
      </Button>
      <Button type="submit" disabled={!dirty || testing} pending={saving}>
        {t('power.connections.save')}
      </Button>
    {/snippet}
  </SettingsPane>
</form>
