import { Server } from "@acme/sdk/server";
import { leftTrim } from "linked-lib";

/** Starts the fixture application. */
export function start(): Server {
  return new Server(leftTrim(" app"));
}

/** Names the protocol revision the application speaks. */
export function revision(): string {
  return Server.revision();
}

/** Keeps the server the application talks through. */
export class App {
  private server: Server = new Server("app");

  /** Tells the client that the application started. */
  async announce(): Promise<void> {
    await this.server.notification({ method: "started" });
  }
}
