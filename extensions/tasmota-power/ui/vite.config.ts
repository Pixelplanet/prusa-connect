import { copyFileSync, cpSync, mkdirSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { moduleBuild, moduleDev } from '@prusa/connect-ui/build';

const BUNDLE_ROOT = fileURLToPath(new URL('./dist/', import.meta.url));
const MANIFEST = fileURLToPath(new URL('../module.yaml', import.meta.url));
const LOCALES = fileURLToPath(new URL('./src/locales/', import.meta.url));

const bundleManifest = () => ({
  name: 'tasmota-power:bundle-manifest',
  buildStart: {
    order: 'pre' as const,
    handler: () => {
      rmSync(BUNDLE_ROOT, { recursive: true, force: true });
      mkdirSync(BUNDLE_ROOT, { recursive: true });
    },
  },
  closeBundle: {
    order: 'post' as const,
    handler: () => {
      copyFileSync(MANIFEST, `${BUNDLE_ROOT}module.yaml`);
      cpSync(LOCALES, `${BUNDLE_ROOT}ui/locales`, { recursive: true });
    },
  },
});

const bundle = moduleBuild({ entry: 'src/index.ts', outDir: 'dist/ui' });

export default ({ command }: { command: 'build' | 'serve' }) =>
  command === 'serve'
    ? moduleDev()
    : { ...bundle, plugins: [...(bundle.plugins ?? []), bundleManifest()] };
