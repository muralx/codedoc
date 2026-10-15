// Probes of receivers typed through declared types: each call carries
// the edge it must yield, as the name called, the tier and the targets
// ("none -" for a call that must stay unresolved).
import { HubClient, AgentStore, Deps, Stores, Box, Peer, Wiring, connect, openHub, anything } from "./hub";
import { Frame, frame } from "lib";

export interface EdgeServerOptions {
  client: HubClient;
  cwd: string;
  frame: Frame;
}

// 1 a property of an interface
export function createEdgeServer(options: EdgeServerOptions): void {
  options.client.register("a", true);     // => register import HubClient.register
  options.cwd.touch();                    // => touch none -
}

// 2 a type alias of an object type, two hops, an optional property, a method signature
export function routes(deps: Deps, wiring: Wiring): void {
  deps.stores.agents.register("a");       // => register import AgentStore.register
  deps.client.register("a", true);        // => register import HubClient.register
  wiring.stores.agents.touch();           // => touch import AgentStore.touch
  const session = deps.open();
  session.close();                        // => close import Session.close
}

// 3 class fields, this.x.y(), a parameter property, a get accessor
export class Service {
  private client: HubClient;
  private deps: Deps;
  constructor(private readonly store: AgentStore, deps: Deps) {
    this.deps = deps;
  }
  run(): void {
    this.client.register("a", true);        // => register import HubClient.register
    this.deps.stores.agents.register("a");  // => register import AgentStore.register
    this.store.register("a");               // => register import AgentStore.register
    this.client.session.close();            // => close import Session.close
    this.store.current.touch();             // => touch import Peer.touch
    const found = this.client.find("x");
    found.touch();                          // => touch import Peer.touch
    const own = this.store;
    own.touch();                            // => touch import AgentStore.touch
  }
}

// 4 a destructured typed parameter, a destructured typed local
export function destructured({ client, frame }: EdgeServerOptions, deps: Deps): void {
  client.register("a", true);             // => register import HubClient.register
  const { stores, client: hub } = deps;
  stores.agents.register("a");            // => register import AgentStore.register
  hub.register("b", false);               // => register import HubClient.register
  const { stores: { agents }, ...rest } = deps;
  agents.touch();                         // => touch import AgentStore.touch
  rest.touch();                           // => touch ambiguous AgentStore.touch|Peer.touch
  frame.widget.grow(1);                   // => grow import Widget.grow
}

// 5 a hop through a type of a dependency
export function dependency(options: EdgeServerOptions, svc: Service): void {
  options.frame.widget.grow(1);           // => grow import Widget.grow
  options.frame.widget.ping();            // => ping import Base.ping
  options.frame.label.touch();            // => touch none -
}

// 6 a hop of unknown type keeps the unique tier
export function unknownHops(s: Stores, box: Box<Peer>): void {
  s.loose.register("a");                  // => register unique AgentStore.register
  s.either.touch();                       // => touch ambiguous AgentStore.touch|Peer.touch
  s.handler.ping();                       // => ping unique Peer.ping
  s.missing.ping();                       // => ping unique Peer.ping
  box.item.ping();                        // => ping unique Peer.ping
  const picked = box.pick();
  picked.ping();                          // => ping ambiguous Peer.ping
}

// 7 the element type of for...of
export function loops(s: Stores, client: HubClient, names: string[], many: Array<Peer>): void {
  for (const p of s.peers) { p.touch(); }          // => touch import Peer.touch
  for (const p of client.peers()) { p.touch(); }   // => touch import Peer.touch
  for (const p of many) { p.touch(); }             // => touch import Peer.touch
  for (const n of names) { n.touch(); }            // => touch none -
  for (const k in s) { k.touch(); }                // => touch ambiguous AgentStore.touch|Peer.touch
  for (const { current } of [s.agents]) { current.touch(); }  // => touch ambiguous AgentStore.touch|Peer.touch
}

// 8 the declared result of a method, awaited or not
export async function results(client: HubClient): Promise<void> {
  const peer = client.find("x");
  peer.touch();                           // => touch import Peer.touch
  const reg = await client.register("a", true);
  reg.confirm();                          // => confirm import Registration.confirm
  const pending = client.register("a", true);
  pending.confirm();                      // => confirm none -
  for (const p of await client.list()) { p.touch(); }  // => touch import Peer.touch
  const copy = peer;
  copy.ping();                            // => ping import Peer.ping
}

function local(): AgentStore { return new AgentStore(); }

// 9 the declared result of a function: imported, of the file, of a dependency
export async function functions(): Promise<void> {
  const c = connect();
  c.register("a", true);                  // => register import HubClient.register
  const h = await openHub();
  h.register("a", true);                  // => register import HubClient.register
  const s = local();
  s.touch();                              // => touch import AgentStore.touch
  const f = frame();
  f.widget.grow(1);                       // => grow import Widget.grow
  const loose = anything();
  loose.touch();                          // => touch ambiguous AgentStore.touch|Peer.touch
  const shadow = (connect: () => Peer): void => {
    const p = connect();
    p.touch();                            // => touch ambiguous AgentStore.touch|Peer.touch
  };
  c.find("x").touch();                    // => touch import Peer.touch
}
