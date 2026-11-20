<!--
  The Home Assistant server that switches plugs this extension has no driver
  for: Kasa, Tapo, Tuya, Matter and the rest. The pane tests and saves its
  own two fields and leaves every other setting as it is saved.
-->
<script lang="ts">
  import {
    Alert,
    AlertDescription,
    AlertTitle,
    Badge,
    Button,
    Field,
    Input,
    SettingsPane,
  } from '@prusa/connect-ui/primitives';

  import type { HATestResult, SettingsResponse } from '../api';
  import FaultNote from '../fault-note.svelte';
  import { heldHost } from '../host';
  import { typed } from '../input';
  import { powerFor, type SettingsChanges } from '../power.svelte';
  import { PANE_BODY } from './pane-body';

  let { saved }: { saved: SettingsResponse } = $props();

  const host = heldHost();
  const t = host.t;
  const power = powerFor(host);

  // The form starts from what is saved, and takes it again after each save.
  // svelte-ignore state_referenced_locally
  let url = $state(saved.settings.ha_url ?? '');
  let token = $state('');

  let saving = $state(false);
  let testing = $state(false);
  let fault = $state.raw<unknown>(undefined);
  let tested = $state.raw<HATestResult | undefined>(undefined);
  let justSaved = $state(false);

  const hasToken = $derived(saved.settings.has_ha_token === true);
  const changes = $derived<SettingsChanges>({
    ha_url: url.trim(),
    ha_token: token.trim(),
  });
  const dirty = $derived(
    token.trim() !== '' || url.trim() !== (saved.settings.ha_url ?? ''),
  );
  const canTest = $derived(
    url.trim() !== '' && (token.trim() !== '' || hasToken),
  );

  const test = async (): Promise<void> => {
    testing = true;
    tested = undefined;
    fault = undefined;
    justSaved = false;
    try {
      const answer = await power.api.testHomeAssistant(
        power.settingsBody(changes),
      );
      tested = answer;
      if (answer.ok) power.keepHaEntities(answer.entities ?? []);
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
      url = answer.settings.ha_url ?? '';
      token = '';
      justSaved = true;
      void power.loadAssignments();
    } catch (thrown) {
      fault = thrown;
    } finally {
      saving = false;
    }
  };
</script>

<form onsubmit={(event) => void save(event)} data-part="ha-pane">
  <SettingsPane
    title={t('power.connections.ha.title')}
    description={t('power.connections.ha.description')}
    edged
  >
    <div class={PANE_BODY}>
      <div class="flex flex-wrap items-center gap-2 text-xs" data-part="status">
        {#if saved.ha_configured}
          <Badge variant="success">{t('power.connections.setUp')}</Badge>
        {:else}
          <Badge variant="outline">{t('power.connections.notSetUp')}</Badge>
        {/if}
        <span class="text-muted-foreground">
          {t('power.connections.usedBy', { count: power.used.ha })}
        </span>
      </div>

      <div class="grid grid-cols-1 gap-4 @md/pane:grid-cols-2">
        <Field
          label={t('power.connections.ha.url')}
          description={saved.ha_url_pinned
            ? t('power.connections.pinned')
            : t('power.connections.ha.urlHint')}
          control={Input}
          controlProps={{
            value: url,
            oninput: typed((value) => (url = value)),
            placeholder: t('power.connections.ha.urlExample'),
            autocomplete: 'off',
            disabled: saved.ha_url_pinned === true,
          }}
        />
        <Field
          label={t('power.connections.ha.token')}
          description={t('power.connections.ha.tokenHint')}
          control={Input}
          controlProps={{
            type: 'password',
            value: token,
            oninput: typed((value) => (token = value)),
            placeholder: hasToken
              ? t('power.field.secretSaved')
              : t('power.connections.ha.tokenExample'),
            autocomplete: 'new-password',
          }}
        />
      </div>

      <FaultNote {fault} />
      {#if tested?.ok}
        <Alert variant="success" data-part="test-ok">
          <AlertDescription>
            {t('power.connections.ha.testOk', {
              count: tested.entities?.length ?? 0,
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
