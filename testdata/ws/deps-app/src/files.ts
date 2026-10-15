import { readFileSync } from "node:fs";
import { join } from "path";

/** Reads the banner the application prints when it starts. */
export function banner(dir: string): string {
  return readFileSync(join(dir, "banner.txt"), "utf8");
}
