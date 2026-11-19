/** The handler an `Input` takes for what a person types into it. */
export const typed =
  (set: (value: string) => void) =>
  (event: Event): void => {
    const target = event.currentTarget;
    if (target instanceof HTMLInputElement) set(target.value);
  };

/** A whole number a person typed, or `fallback` for anything else. */
export const wholeOf = (text: string, fallback: number): number => {
  const parsed = Number.parseInt(text, 10);

  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
};
