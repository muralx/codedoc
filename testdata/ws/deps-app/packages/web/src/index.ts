import { dup } from "dup";

/** Renders the page title. */
export function title(): string {
  return dup("web").join(" ");
}
