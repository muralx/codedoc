package ts_test

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/ts"
)

// apiOf extracts the public API of a declaration file.
func apiOf(t *testing.T, path, src string) *extract.FileResult {
	t.Helper()
	res, err := ts.New("typescript").ExtractAPI(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func qualifiedOf(syms []extract.Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.Qualified
	}
	return out
}

const serverDTS = `import { Protocol } from "../shared/protocol.js";
export * from "./types.js";
export { Helper as Aid } from './helper.js';
/**
 * An MCP server on top of a pluggable transport.
 */
export declare class Server extends Protocol {
    private _capabilities;
    /** Registered handlers. */
    protected handlers: Map<string, unknown>;
    readonly name: string;
    constructor(name: string);
    /**
     * Sends a notification to the client.
     */
    notification(n: Notification): Promise<void>;
    private assertReady;
}
declare class Hidden {
    run(): void;
}
declare function internal(): void;
/** Options of a server. */
export interface ServerOptions {
    capabilities?: string[];
    close(): void;
}
export type Mode = "stdio" | "http";
export declare const VERSION = "1.2.0";
export declare function connect(url: string): Server;
export declare enum Level { Debug, Info }
`

func TestExtractAPIKeepsExportedDeclarationsAndTheirMembers(t *testing.T) {
	res := apiOf(t, "dist/server.d.ts", serverDTS)
	want := []string{
		"dist/server.d.ts/Server", "dist/server.d.ts/Server.handlers", "dist/server.d.ts/Server.name",
		"dist/server.d.ts/Server.constructor", "dist/server.d.ts/Server.notification",
		"dist/server.d.ts/ServerOptions", "dist/server.d.ts/ServerOptions.capabilities",
		"dist/server.d.ts/ServerOptions.close", "dist/server.d.ts/Mode", "dist/server.d.ts/VERSION",
		"dist/server.d.ts/connect", "dist/server.d.ts/Level",
	}
	if got := qualifiedOf(res.Symbols); !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	byName := map[string]extract.Symbol{}
	for _, s := range res.Symbols {
		byName[s.Name] = s
	}
	n := byName["notification"]
	if docText(n) != "Sends a notification to the client." || n.SigText != "notification(n: Notification): Promise<void>" ||
		n.Kind != extract.KindMethod || n.Container != "dist/server.d.ts/Server" {
		t.Errorf("notification: %+v", n)
	}
	if docText(byName["Server"]) != "An MCP server on top of a pluggable transport." {
		t.Errorf("class doc: %q", docText(byName["Server"]))
	}
	if len(res.References) != 1 || res.References[0].Kind != extract.RefExtend {
		t.Errorf("API mode extracts supertypes only: %+v", res.References)
	}
	var raws []string
	for _, imp := range res.Imports {
		raws = append(raws, imp.Raw)
	}
	if !reflect.DeepEqual(raws, []string{"../shared/protocol.js", "./types.js", "./helper.js"}) {
		t.Errorf("imports: %v", raws)
	}
}

// Bundled declaration files declare everything first and export by name
// at the end.
func TestExtractAPIFollowsExportClausesDefaultAndExportEquals(t *testing.T) {
	src := `declare class Client { send(): void; protected retry(): void; private x; }
declare class Unused { run(): void; }
declare function create(): Client;
declare const fallback: Client, other: number;
declare namespace z { type Infer = string; }
declare namespace hidden { type T = string; }
export { Client, create as make, z };
export default fallback;
`
	got := qualifiedOf(apiOf(t, "index.d.ts", src).Symbols)
	want := []string{"index.d.ts/Client", "index.d.ts/Client.send", "index.d.ts/Client.retry", "index.d.ts/create",
		"index.d.ts/fallback", "index.d.ts/other", "index.d.ts/z.Infer"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	cjs := "declare function lib(x: string): string;\ndeclare namespace lib { interface Opts { a: number } }\nexport = lib;\n"
	got = qualifiedOf(apiOf(t, "index.d.ts", cjs).Symbols)
	if !reflect.DeepEqual(got, []string{"index.d.ts/lib", "index.d.ts/lib.Opts", "index.d.ts/lib.Opts.a"}) {
		t.Fatalf("export =: %v", got)
	}
}

// A declaration file without imports and exports declares globals and
// ambient modules: everything in it is visible.
func TestExtractAPITreatsAScriptAsAllPublic(t *testing.T) {
	src := "/** Global helper. */\ndeclare function helper(): void;\ninterface Env { HOME: string }\n" +
		"declare module \"fs\" {\n  function readFile(p: string): string;\n}\n"
	res := apiOf(t, "globals.d.ts", src)
	want := []string{"globals.d.ts/helper", "globals.d.ts/Env", "globals.d.ts/Env.HOME", "globals.d.ts/fs.readFile"}
	if got := qualifiedOf(res.Symbols); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	for _, s := range res.Symbols {
		if s.Visibility != extract.Public {
			t.Errorf("%s is %s", s.Qualified, s.Visibility)
		}
	}
}

func TestExtractAPIRegistersForEveryTSLanguage(t *testing.T) {
	for _, lang := range []string{"typescript", "tsx", "javascript"} {
		if _, ok := extract.APIFor(lang); !ok {
			t.Errorf("no API extractor for %s", lang)
		}
	}
	if _, ok := extract.APIFor("python"); ok {
		t.Error("python has no API mode")
	}
	if _, err := ts.New("cobol").ExtractAPI("a.ts", nil); err == nil {
		t.Error("unknown grammar must fail")
	}
}

func TestExtractAPIReturnsTheSupertypesOfKeptDeclarations(t *testing.T) {
	res := apiOf(t, "server/index.d.ts", serverDTS+`
export interface Wide extends Narrow<string>, ns.Base { extra: number }
declare class Hidden2 extends Server {}
`)
	type super struct{ sub, name, qualifier string }
	var got []super
	for _, r := range res.References {
		if r.Kind != extract.RefExtend {
			t.Errorf("only extend references are returned: %+v", r)
		}
		got = append(got, super{r.Enclosing, r.Name, r.Qualifier})
	}
	want := []super{
		{"server/index.d.ts/Server", "Protocol", ""},
		{"server/index.d.ts/Wide", "Narrow", ""},
		{"server/index.d.ts/Wide", "Base", "ns"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("supertypes: got %+v, want %+v", got, want)
	}
}
