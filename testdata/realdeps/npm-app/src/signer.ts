import { HMAC } from "@noble/hashes/hmac";
import { sha256 } from "@noble/hashes/sha256";
import { bytesToHex } from "@noble/hashes/utils";
import ms from "ms";

/** Signs messages with a keyed hash. */
export class Signer {
  private mac: HMAC<any> = new HMAC(sha256, "fixture-key");

  /** Returns the keyed state as it is now, so signing never consumes it. */
  snapshot(): HMAC<any> {
    return this.mac.clone();
  }

  /** Returns the hex signature of a message. */
  sign(message: string): string {
    return bytesToHex(this.snapshot().update(message).digest());
  }
}

/** Says how long a signature stays valid, in milliseconds. */
export function lifetime(): number {
  return ms("2 days");
}
