package ts_test

import (
	"strconv"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/ts"
)

// recvTypes returns, by "name@line", the receiver type recorded on each
// qualified call of a source file.
func recvTypes(t *testing.T, lang, src string) map[string]string {
	t.Helper()
	res, err := ts.New(lang).Extract("src/a.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range res.References {
		if r.Qualifier != "" && r.Kind == extract.RefCall {
			out[r.Name+"@"+strconv.Itoa(r.Line)] = r.RecvType
		}
	}
	return out
}

func checkRecvTypes(t *testing.T, got, want map[string]string) {
	t.Helper()
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("%s: receiver type %q (found %v), want %q", k, g, ok, w)
		}
	}
}

func TestLocalVariableTypesAreRecorded(t *testing.T) {
	got := recvTypes(t, "typescript", `import { Server } from "sdk";
import * as ns from "sdk";

export function start(sink: Sink, opt?: Options, n: number, list: Item[], gen: Box<Item>): void {
  sink.send();
  opt.read();
  n.toFixed();
  list.push();
  gen.open();
  const server = new Server("x");
  let typed: Wide = new Server("y");
  var plain: ns.Server = make();
  const far = new ns.Deep.Server();
  const loose = make();
  server.connect();
  typed.connect();
  plain.connect();
  far.connect();
  loose.connect();
  server.inner.close();
  const cb = (peer: Peer) => peer.ping();
  const { a, b } = pair();
  a.use();
}

export function generic<T extends Base>(x: T, y: Array<T>): void {
  x.run();
  y.pop();
}
`)
	checkRecvTypes(t, got, map[string]string{
		"send@5": "Sink", "read@6": "Options", "toFixed@7": "-", "push@8": "-", "open@9": "Box",
		"connect@15": "Server", "connect@16": "Server", "connect@17": "ns.Server", "connect@18": "ns.Deep.Server",
		"connect@19": "make()", "close@20": "Server", "ping@21": "Peer", "use@23": "pair()#a", "run@27": "?", "pop@28": "-",
	})
}

func TestLaterDeclarationShadowsALocalVariable(t *testing.T) {
	got := recvTypes(t, "javascript", `export function run(x) {
  x.zero();
  let s = new Store();
  s.first();
  {
    const s = other();
    s.second();
  }
  for (const s of items) {
    s.third();
  }
  var t = new Store();
  t = other();
  t.fourth();
}

export function next() {
  s.fifth();
}
`)
	checkRecvTypes(t, got, map[string]string{
		"zero@2": "?", "first@4": "Store", "second@7": "other()", "third@10": "?", "fourth@14": "Store", "fifth@18": "",
	})
}

func TestClassPropertyTypesAreRecorded(t *testing.T) {
	got := recvTypes(t, "typescript", `export class App {
  private declared: Server;
  private built = new Server("app");
  count = 1;

  constructor(private readonly store: Store) {
    this.late = new Client();
    this.count = other();
    this.late.dial();
  }

  run(): void {
    this.built.start();
    this.late.send();
    this.declared.stop();
    this.count.toFixed();
    this.store.get();
    this.built.inner.deep();
  }
}

export class Other {
  run(): void {
    this.built.start();
  }
}
`)
	checkRecvTypes(t, got, map[string]string{
		"dial@9": "Client", "start@13": "Server", "send@14": "Client", "stop@15": "", "toFixed@16": "",
		"get@17": "Store", "deep@18": "Server", "start@24": "",
	})
}

// scopesSrc declares variables of one name in nested scopes.
const scopesSrc = `import { Alpha, Beta } from "./kit";
const lib = require("lib");

export const store = new Alpha();

type Handler = (store: Beta) => void;
interface Sink { put(store: Beta): void }

export function scopes(items: Beta[], list: { x: Beta }[], h: { x: Beta }): void {
  const x = new Alpha();
  {
    const x = new Beta();
    x.block();
  }
  x.afterBlock();
  items.forEach(x => x.bare());
  list.forEach(({ x }) => x.destructured());
  list.forEach((x: Beta) => x.typed());
  x.afterClosures();
  if (h) {
    const { x } = h;
    x.pattern();
    const [p, { q: r }, ...rest] = many();
    r.nested();
    rest.spread();
  }
  for (const x of items) { x.loop(); }
  for (const key in h) { key.inLoop(); }
  try { x.inTry(); } catch (x) { x.caught(); }
  x.afterLoops();
  function inner(x: Beta) { x.inner(); }
  x.afterInner();
  let z: Alpha | Beta = new Alpha();
  z.before();
  z = new Beta();
  z.after();
  let y: Alpha;
  y = make() as never;
  y.kept();
  let same = new Alpha();
  same = new Alpha();
  same.kept();
  const cb = () => { w.early(); };
  const w = new Alpha();
  store.top();
  later.below();
  lib.required();
}

const later = new Beta();
`

func TestVariablesLiveInLexicalScopes(t *testing.T) {
	got := recvTypes(t, "typescript", scopesSrc)
	checkRecvTypes(t, got, map[string]string{
		"block@13": "Beta", "afterBlock@15": "Alpha", "bare@16": "?", "destructured@17": "?", "typed@18": "Beta",
		"afterClosures@19": "Alpha", "pattern@22": "?", "nested@24": "?", "spread@25": "?", "loop@27": "Beta",
		"inLoop@28": "?", "inTry@29": "Alpha", "caught@29": "?", "afterLoops@30": "Alpha", "inner@31": "Beta",
		"afterInner@32": "Alpha", "before@34": "Alpha", "after@36": "?", "kept@39": "Alpha", "kept@42": "Alpha",
		"early@43": "", "top@45": "Alpha", "below@46": "Beta", "required@47": "",
	})
}
