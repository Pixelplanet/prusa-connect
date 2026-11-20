// The box every settings pane of this module draws its content in.
//
// The library's SettingsPane pads nothing but its heading and its description:
// what stands in it brings its own padding, in the theme's panel steps, so the
// content lines up with the description above it and the buttons below. The
// top padding is the smaller section step, because the description already
// stands above it.
//
// A core older than these theme steps emits no rule for them, and its pane
// padded its own body, so the same markup reads right on both.
export const PANE_BODY =
  'flex flex-col gap-4 p-panel-padding-mobile pt-section-gap @lg/pane:p-panel-padding @lg/pane:pt-section-gap';
