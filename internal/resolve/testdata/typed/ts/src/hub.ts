// The types the typed-receiver probes call through. register and touch
// are declared twice, so a receiver that is not typed leaves them
// ambiguous; ping, confirm and close are declared once.
export class Registration { confirm(): void {} }
export class Peer { ping(): void {} touch(): void {} }
export class Session { close(): void {} }

export class HubClient {
  register(input: string, flag: boolean): Promise<Registration> { return Promise.resolve(new Registration()); }
  peers(): Peer[] { return []; }
  find(id: string): Peer | undefined { return undefined; }
  list(): Promise<Peer[]> { return Promise.resolve([]); }
  get session(): Session { return new Session(); }
}

export class AgentStore {
  current = new Peer();
  register(input: string): void {}
  touch(): void {}
}

export interface Stores {
  agents: AgentStore;
  peers: Peer[];
  loose: any;
  either: AgentStore | Peer;
  handler: (input: string) => Peer;
}

export type Deps = {
  stores: Stores;
  client?: HubClient | undefined;
  open(): Session;
};

export type Wiring = Deps;

export interface Box<T> { item: T; pick<U>(): U; }

export function connect(): HubClient { return new HubClient(); }
export const openHub = async (): Promise<HubClient> => new HubClient();
export function anything() { return new HubClient(); }
