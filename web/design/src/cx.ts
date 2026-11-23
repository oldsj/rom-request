/** Joins class names, skipping empty ones. A caller's className extends a component's classes. */
export function cx(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(' ');
}
