package ts_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/ts"
)

// typePathsSrc reaches variables through declared property types,
// function and method results, destructuring and loop elements.
const typePathsSrc = `import { load } from "./io";

export class App {
  private client: Client;
  constructor(private readonly store: Store, public items: Item[], plain: Plain, readonly [a]: Odd) {}
  run(): void {
    const s = this.store;
    s.get();
    const c = this.client;
    c.send();
    const r = this.client.find("x");
    r.touch();
    for (const it of this.items) { it.touch(); }
    this.plain.touch();
  }
}

export async function use({ client, frame: { widget }, ...rest }: Options, deps: Deps, opt: Deps | undefined): Promise<void> {
  client.send();
  widget.grow();
  rest.none();
  const { stores, bus: events = fallback, [key]: computed } = deps;
  stores.agents.get();
  events.emit();
  computed.none();
  opt.stores.get();
  for (const p of deps.peers) { p.ping(); }
  for (const q of (await deps.client.list())!) { q.ping(); }
  for (const [k, v] of deps.pairs) { v.none(); }
  for (const n of opt.names as string[]) { n.none(); }
  const made = load();
  made.use();
  const waited = await load();
  waited.use();
  const copy = stores;
  copy.agents.get();
  const hidden = (load: () => Thing) => { const t = load(); t.none(); };
  const arr: ReadonlyArray<Item> = [];
  for (const i of arr) { i.touch(); }
  const ro: readonly Item[] = [];
  ro.map();
  const m: Map<string, Item> = new Map();
  m.get();
  const u: A | B = pick();
  u.none();
  const fn = deps.make;
  fn().none();
}
`

func TestTypePathsAreRecordedFromDeclaredTypes(t *testing.T) {
	got := recvTypes(t, "typescript", typePathsSrc)
	checkRecvTypes(t, got, map[string]string{
		"get@8": "Store", "send@10": "this#client", "touch@12": "this#client#find()", "touch@13": "Item",
		"touch@14": "", "send@19": "Options#client", "grow@20": "Options#frame#widget", "none@21": "?",
		"get@23": "Deps#stores", "emit@24": "Deps#bus", "none@25": "?", "get@26": "Deps",
		"ping@27": "Deps#peers@", "ping@28": "Deps#client#list()^@", "none@29": "?", "none@30": "?",
		"use@32": "load()", "use@34": "load()^", "get@36": "Deps#stores", "none@37": "?",
		"touch@39": "Item", "map@41": extract.BuiltinType, "get@43": extract.BuiltinType, "none@45": "?",
	})
}

func TestObjectTypeAliasesHaveMembers(t *testing.T) {
	res, err := ts.New("typescript").Extract("src/a.ts", []byte(`export type Deps = {
  stores: Stores;
  open(): Session;
};
type Plain = Stores;
type Both = Stores & { extra: number };
`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]extract.Kind{}
	for _, s := range res.Symbols {
		got[s.Qualified] = s.Kind
	}
	want := map[string]extract.Kind{
		"src/a.ts/Deps": extract.KindTypeAlias, "src/a.ts/Deps.stores": extract.KindField,
		"src/a.ts/Deps.open": extract.KindMethod, "src/a.ts/Plain": extract.KindTypeAlias, "src/a.ts/Both": extract.KindTypeAlias,
	}
	if len(got) != len(want) {
		t.Errorf("symbols %v, want %v", got, want)
	}
	for q, k := range want {
		if got[q] != k {
			t.Errorf("%s: kind %q, want %q", q, got[q], k)
		}
	}
}
