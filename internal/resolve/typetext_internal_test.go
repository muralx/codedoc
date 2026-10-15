package resolve

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

func TestDeclaredText(t *testing.T) {
	for _, tc := range []struct {
		fam       string
		step      extract.TypeStepKind
		kind, sig string
		want      string
	}{
		{"ts", extract.StepField, "field", "client: HubClient;", "HubClient"},
		{"ts", extract.StepField, "field", "private readonly store: AgentStore = new AgentStore();", "AgentStore"},
		{"ts", extract.StepField, "field", "env?: Env | undefined;", "Env | undefined"},
		{"ts", extract.StepField, "field", "handler: (input: unknown) => Promise<unknown>;", "(input: unknown) => Promise<unknown>"},
		{"ts", extract.StepField, "field", "built = new ns.Widget<Item>();", "ns.Widget"},
		{"ts", extract.StepField, "field", "count = 1;", ""},
		{"ts", extract.StepField, "field", `"quoted-name": Item;`, ""},
		{"ts", extract.StepField, "method", "get session(): Session {", "Session"},
		{"ts", extract.StepField, "method", "session(): Session {", ""},
		{"ts", extract.StepResult, "method", "async find(id: string): Promise<Peer | undefined> {", "Promise<Peer | undefined>"},
		{"ts", extract.StepResult, "method", "list(): Peer[];", "Peer[]"},
		{"ts", extract.StepResult, "method", "run() {", ""},
		{"ts", extract.StepResult, "method", "broken(a: string", ""},
		{"ts", extract.StepResult, "method", "nothing", ""},
		{"ts", extract.StepResult, "field", "client: HubClient;", ""},
		{"go", extract.StepField, "field", "items []*Item", "[]*Item"},
		{"go", extract.StepField, "field", "*store.Store", "*store.Store"},
		{"go", extract.StepField, "method", "func (s *Store) Get() *Item", ""},
		{"go", extract.StepResult, "method", "func (s *Store) Get(id string) (*Item, error)", "*Item"},
		{"go", extract.StepResult, "method", "func (s *Store) All() (items []Item, err error)", "[]Item"},
		{"go", extract.StepResult, "method", "func (s *Store) Wait() (chan Item, error)", "chan Item"},
		{"go", extract.StepResult, "method", "Get(id string) *Item", "*Item"},
		{"go", extract.StepResult, "method", "func (s *Store Get()", ""},
		{"go", extract.StepResult, "method", "func (s *Store) Close()", ""},
		{"java", extract.StepField, "field", "private final Store store", "Store"},
		{"java", extract.StepResult, "method", "Store get()", ""},
		{"py", extract.StepField, "field", "self.store = store", ""},
	} {
		if got := declaredText(tc.fam, tc.step, tc.kind, tc.sig); got != tc.want {
			t.Errorf("%s %s %q: %q, want %q", tc.fam, tc.kind, tc.sig, got, tc.want)
		}
	}
}

func TestArrowFunctionResultText(t *testing.T) {
	if got := tsResultText("export const open = async (): Promise<Hub> =>"); got != "Promise<Hub>" {
		t.Errorf("arrow function result: %q", got)
	}
}

func TestTypeNameOf(t *testing.T) {
	for _, tc := range []struct{ fam, text, want string }{
		{"ts", "HubClient", "HubClient"},
		{"ts", "HubClient | undefined", "HubClient"},
		{"ts", "null | readonly Store<Item>", "Store"},
		{"ts", "ns.Store", "ns.Store"},
		{"ts", "A | B", ""},
		{"ts", "Map<string, A | B> | undefined", "Map"},
		{"ts", "any", ""},
		{"ts", "unknown", ""},
		{"ts", "Item[]", ""},
		{"ts", "(a: A) => B", ""},
		{"ts", "{ a: A }", ""},
		{"go", "*store.Store", "store.Store"},
		{"go", "Set[string]", "Set"},
		{"go", "[]Item", ""},
		{"go", "func() error", ""},
		{"java", "Store", "Store"},
		{"java", "int[]", ""},
		{"py", "Store", ""},
	} {
		if got := typeNameOf(tc.fam, tc.text); got != tc.want {
			t.Errorf("%s %q: %q, want %q", tc.fam, tc.text, got, tc.want)
		}
	}
}

func TestElemAndAwaitedText(t *testing.T) {
	for _, tc := range []struct{ fam, text, want string }{
		{"go", "[]*Item", "*Item"},
		{"go", "[4]Item", "Item"},
		{"go", "map[string][]Item", "[]Item"},
		{"go", "map[Key[int]]Item", "Item"},
		{"go", "*Item", ""},
		{"go", "[broken", ""},
		{"ts", "Peer[]", "Peer"},
		{"ts", "Peer[] | undefined", "Peer"},
		{"ts", "readonly Peer[][]", "Peer[]"},
		{"ts", "Array<Peer>", "Peer"},
		{"ts", "Set<Peer>", "Peer"},
		{"ts", "Map<string, Peer>", ""},
		{"ts", "Array<Pair<A, B>>", "Pair<A, B>"},
		{"ts", "Peer", ""},
		{"java", "List<Peer>", ""},
	} {
		if got := elemText(tc.fam, tc.text); got != tc.want {
			t.Errorf("element of %s %q: %q, want %q", tc.fam, tc.text, got, tc.want)
		}
	}
	for text, want := range map[string]string{
		"Promise<Peer[]>": "Peer[]", "Promise<Hub> | undefined": "Hub", "Hub": "Hub", "Array<Promise<Hub>>": "Array<Promise<Hub>>",
	} {
		if got := awaitedText(text); got != want {
			t.Errorf("awaited %q: %q, want %q", text, got, want)
		}
	}
}

func TestDeclaresParam(t *testing.T) {
	for _, tc := range []struct {
		fam, sig, name string
		member, want   bool
	}{
		{"ts", "export interface Box<T, U extends Base = Item> {", "U", false, true},
		{"ts", "export interface Box<T> {", "Item", false, false},
		{"ts", "export class Store extends Base<Item> {", "Item", false, false},
		{"ts", "type Pair<A, B> = {", "B", false, true},
		{"ts", "pick<U>(): U;", "U", true, true},
		{"ts", "private async pick<K extends keyof T>(key: K): K {", "K", true, true},
		{"ts", "items: Array<Item>;", "Item", true, false},
		{"ts", "broken<T(): T", "T", true, false},
		{"go", "type Box[T any, U comparable] struct", "U", false, true},
		{"go", "Box[T any] struct", "T", false, true},
		{"go", "type Box struct", "T", false, false},
		{"go", "func (b *Box[T]) Get() T", "T", true, false},
		{"go", "items []Item", "Item", true, false},
	} {
		if got := declaresParam(tc.fam, tc.sig, tc.name, tc.member); got != tc.want {
			t.Errorf("%s %q declares %s: %v", tc.fam, tc.sig, tc.name, got)
		}
	}
}
