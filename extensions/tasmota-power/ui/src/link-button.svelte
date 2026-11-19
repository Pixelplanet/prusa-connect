<!--
  A link to another screen, drawn as a Button. The Button draws an anchor in
  place of its own element, so the link looks like every other one and still
  opens in a new tab when a person asks for that.
-->
<script lang="ts">
  import { Button, type ButtonProps } from '@prusa/connect-ui/primitives';
  import type { Snippet } from 'svelte';

  let {
    href,
    variant = 'link',
    size,
    children,
  }: {
    href: string;
    variant?: ButtonProps['variant'];
    size?: 'sm' | 'md' | 'lg';
    children: Snippet;
  } = $props();
</script>

<Button {variant} {...size ? { size } : {}}>
  {#snippet child({ props })}
    <a {...props} {href}>{@render children()}</a>
  {/snippet}
</Button>
