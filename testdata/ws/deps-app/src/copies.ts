import { dup } from "dup";

/** Repeats a label as often as the application needs it. */
export function copies(label: string): string[] {
  return dup(label, 3);
}
