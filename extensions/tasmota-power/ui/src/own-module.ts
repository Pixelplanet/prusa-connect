export const OWN_MODULE = decodeURIComponent(
  /\/modules\/([^/]+)\/ui\//u.exec(import.meta.url)?.[1] ?? '',
);
