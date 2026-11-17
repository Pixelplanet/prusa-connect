import { createApi } from './api';
import { heldHost } from './host';

export default async (deviceId: string): Promise<void> => {
  const host = heldHost();
  const api = createApi(host);
  const current = await api.getStatus(deviceId);
  if (!current.config.tasmota_topic) {
    throw new Error(host.t('tasmota-power.action.noTopic'));
  }
  await api.sendPower(deviceId, 'toggle');
};
