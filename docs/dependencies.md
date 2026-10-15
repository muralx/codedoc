# Dependencies

codedoc also indexes the third-party packages the code of a workspace
builds on, so an agent asks the index for an API instead of grepping
`node_modules/` or the Go module cache:

- **What.** The packages `package.json` (npm, pnpm and yarn layouts) and
  `go.mod` declare directly **and a source file imports**, at the
  version that is installed. Only the public API is read: exported
  declarations with their signatures and doc comments, and the package
  README. Nothing inside a package is resolved, except which class
  extends which, so inherited members are found.
- **Imported only.** A declared package no source file imports is
  listed as `unused` and not indexed: a linter or a bundler says
  nothing about what the code builds on. Imports of tool configuration
  files (`eslint.config.js`, `vite.config.ts`, dot files) do not count.
  `deps.include` indexes a package anyway.
- **`@types`.** When a package ships no types and `@types/<name>` is
  installed, its declarations are read as the package's. An `@types`
  package of its own is indexed when the package it declares is
  imported. `@types/node` is indexed module by module: only the
  built-in modules the code imports (`node:fs`, `fs`), shown as
  `modules:N`.
- **Where.** One shared database, `deps.db` next to the workspace
  databases, holds each package version once for every workspace. A
  workspace sees exactly the versions its own manifests resolve to.
- **When.** `codedoc index`, `codedoc deps index` and the daemon index
  what is missing. A query does too, for at most two seconds; what is left
  shows as `deps: N pending` on answers that used dependencies.

The examples run on the workspace `fixture-deps` of the bundled fixtures
(`deps-app` with a fake `node_modules`, `deps-go` with a fake module
cache), copied out as in the [Quickstart](../README.md#quickstart):

```sh
export CODEDOC_GOMODCACHE=/tmp/ws/gomodcache
cd /tmp/ws/deps-app && codedoc index --verbose
```

```
deps-app@f4292bb deps-go@a414542
== index (2)
deps-app  main@f4292bb files:+6 ~0 -0
deps-go  main@a414542 files:+2 ~0 -0
== deps (2)
deps-app  indexed:5 pending:0 missing:1 failed:0 skipped:1 unused:3
deps-go  indexed:1 pending:0 missing:1 failed:0 skipped:0 unused:2
indexed 6 package version(s)
```

| Command | Flags | Answers |
|---|---|---|
| `api <package> [name]` | `--doc` | what a package offers: its modules and exported names, or one type with its members, the inherited ones included |
| `find`, `symbol`, `outline`, `callers` | — | a name no workspace symbol has is looked up in the dependencies without a flag; the entry is tagged `[dep name@version]` |
| `find`, `symbol`, `search`, `where`, `explain`, `outline`, `how` | `--deps` | the same answers with the dependencies included, also for a name the workspace has |
| `outline <package>` | — | the modules of a package (its name, `name@version` or id); `outline <package>/<subpath>` lists one module's symbols; `outline <type>` a type's members, then the inherited ones |
| `explain <package>` | — | the README lead and the symbols of the package the workspace uses most |
| `callers <dep symbol>`, `refs <dep symbol>` | as for workspace symbols | the workspace code that calls or refers to a dependency symbol |
| `tests <dep symbol>`, `impact <dep symbol>` | as for workspace symbols | the tests of the workspace that use it (`--transitive`: through the workspace code that does); its workspace callers, references and their tests in one answer |
| `callees <addr>` | — | calls into a dependency are listed with the tag `[dep]` |
| `overview` | — | one more section, `dependencies`: package, state and symbol count per repo |
| `deps` | `--repo`, `--workspace`, `--budget`, `--offset`, `--out` | every manifest's packages with their state |
| `deps index` | `--embed`, `--verbose`, `--repo`, `--workspace` | discover again, index what `deps.db` lacks, retry failed packages. Silent on success |
| `deps prune` | `--all`, `--dry-run`, `--verbose` | delete the package versions no workspace uses: neither a workspace of the config nor a workspace database recorded in `deps.db` as a user, so a run under another config is safe; `--dry-run` only lists them; `--all` empties `deps.db`. Silent on success |

**`api`** answers "what does this dependency offer" in one call. With a
package: the first sentence of its README, the exported names of its
main module and of the modules the workspace imports most, and the list
of its modules. With a name: that type or function with its doc and,
for a type, its members with their signatures.

```
$ codedoc api @acme/sdk
deps-app@f4292bb deps-go@a414542
@acme/sdk@1.2.0  /tmp/ws/deps-app/node_modules/@acme/sdk  Client and server building blocks for the Acme protocol.
== @acme/sdk (1)
PROTOCOL_VERSION  const
== @acme/sdk/server (1)
Server  class
== @acme/sdk/shared/protocol.js (1)
Protocol  class
== modules of @acme/sdk (6)
@acme/sdk  1 name
@acme/sdk/locales/en.js  1 name
@acme/sdk/server  1 name
@acme/sdk/server/transport.js  1 name
@acme/sdk/shared/protocol.js  1 name
@acme/sdk/types.js  2 names

$ codedoc api @acme/sdk Server --doc
deps-app@f4292bb deps-go@a414542
Server  /tmp/ws/deps-app/node_modules/@acme/sdk/dist/server/index.d.ts:8-14  export declare class Server extends Protocol  An Acme server on top of a pluggable transport.  [dep @acme/sdk@1.2.0] [from @acme/sdk/server]
== members of Server (6)
dist/server/index.d.ts
  constructor  :10  (name: string)
  connect  :12  (transport: Transport): Promise<Result>  Attaches the server to a transport.
  onClose  :13  protected (): void
dist/shared/protocol.d.ts
  Protocol.notification  :9  (notification: Notification): Promise<void>  Sends a notification to the other side.
  Protocol.close  :11  (): Promise<void>  Closes the connection.
  Protocol.revision  :13  static (): string  The protocol revision an endpoint class speaks.
```

`Server` declares no `notification`: it inherits it from `Protocol`, and
`api` lists it under the file that declares it. A name the package does
not export gets the nearest names, and a declared package that is not
indexed says why and what to do:

```
$ codedoc api @acme/sdk Servr
deps-app@f4292bb deps-go@a414542
== nearest names in @acme/sdk (1)
Server  @acme/sdk/server  class
@acme/sdk exports no Servr; the nearest names are listed

$ codedoc api lint-tool
deps-app@f4292bb deps-go@a414542
lint-tool@9.4.0 is declared in package.json but no source file imports it, so it is not indexed; name it under deps.include in the codedoc config and run codedoc deps index

$ codedoc api left-pad
deps-app@f4292bb deps-go@a414542
left-pad is declared in package.json but not installed; install the dependencies and run codedoc deps index
```

A dependency symbol is addressed like any other. The address through
the class works for an inherited member, and the source is read from the
directory the package is installed in:

```
$ codedoc symbol Server.notification
deps-app@f4292bb deps-go@a414542
Protocol.notification  /tmp/ws/deps-app/node_modules/@acme/sdk/dist/shared/protocol.d.ts:9  [dep @acme/sdk@1.2.0]
6	    /**
7	     * Sends a notification to the other side.
8	     */
9	    notification(notification: Notification): Promise<void>
Server.notification is declared on Protocol
```

**Edges from workspace code.** A reference that an import binds to a
package of the nearest manifest declaring it becomes an edge to the
dependency symbol: a named or namespace import in TypeScript, a package
name in Go, and a member reached through a field, a local variable or a
parameter whose type is a dependency type (`const server = new Server()`
then `server.connect()`; `c := kit.New()` then `c.Do()`, see
[Local variables](cli.md#graph)). A member of a namespace the package exports
resolves too (`import { z } from 'zod'` or `import * as z from 'zod'`,
then `z.object(...)`): codedoc follows the package's re-exports to the
file that declares it. A use through an inherited member lands on the
symbol that declares it.

```
$ codedoc callers Server.revision
deps-app@f4292bb
== callers of Protocol.revision [dep @acme/sdk@1.2.0] (1)
revision  src/main.ts:11  return Server.revision();
Server.revision is declared on Protocol

$ codedoc callees App.announce
deps-app@f4292bb
== callees of App.announce (1)
Protocol.notification  src/main.ts:20  await this.server.notification({ method: "started" });  [dep]
```

When a manifest moves to another version of a package, the next sync
indexes that version and resolves the importing files again. Two
manifests of one workspace may use two versions: both are searched, each
entry carries its version, and a file's edges follow its nearest
manifest.

**States.** `codedoc deps` lists every package of every manifest:

```
$ codedoc deps
deps-app@f4292bb deps-go@a414542
== deps (14)
npm/@acme/sdk@1.2.0  deps-app:package.json  indexed declared:^1.2.0 symbols:18 vectors:0/14
npm/@fixture/web  deps-app:package.json  skipped declared:workspace:*
npm/@types/node@22.1.0  deps-app:package.json  indexed declared:^22.0.0 symbols:4 vectors:0/5 modules:2
npm/dup@2.0.0  deps-app:package.json  indexed declared:^2.0.0 symbols:2 vectors:0/2
npm/left-pad  deps-app:package.json  missing declared:^1.3.0
npm/linked-lib@0.3.0  deps-app:package.json  indexed declared:^0.3.0 symbols:2 vectors:0/2
npm/lint-tool@9.4.0  deps-app:package.json  unused declared:^9.0.0
npm/plain-js@2.0.1  deps-app:package.json  unused declared:^2.0.0
npm/@acme/sdk@1.2.0  deps-app:packages/web/package.json  unused declared:^1.2.0
npm/dup@1.0.0  deps-app:packages/web/package.json  indexed declared:^1.0.0 symbols:1 vectors:0/1
go/github.com/BurntSushi/toml@v0.3.1  deps-go:go.mod  unused declared:v0.3.1
go/github.com/acme/absent  deps-go:go.mod  missing declared:v0.1.0
go/github.com/acme/kit@v1.4.0  deps-go:go.mod  indexed declared:v1.4.0 symbols:16 vectors:0/17
go/github.com/fixture/lib@local-616361a2  deps-go:go.mod  unused declared:v0.0.0
5 unused: declared but imported by no source file, so not indexed; name a package in deps.include to index it anyway
```

| State | Meaning |
|---|---|
| `indexed` | in `deps.db`, with its symbol count and `vectors:N/M` |
| `pending` | installed, not indexed yet; `codedoc deps index` does it |
| `missing` | declared, not installed |
| `failed` | indexing failed; the line carries the error |
| `skipped` | a package of the workspace itself |
| `unused` | installed, but no source file below that manifest imports it. The state is per manifest: `packages/web` declares the sdk without importing it |

**Embedding dependencies.** `vectors:N/M` on a package counts its
embedded symbols and README. `codedoc index` and `codedoc deps index`
do not embed dependencies: `codedoc deps index --embed` does, and the
daemon does it in the background. Without a model `--embed` fails in
one line that says why (`deps index --embed: nothing was embedded: no
embedding model is configured; ...`). Until then `search --deps` finds
dependency symbols by keywords. With a model configured:

```
$ codedoc deps index --embed --verbose
...
npm/@acme/sdk@1.2.0  deps-app:package.json  indexed declared:^1.2.0 symbols:18 vectors:14/14
...
embedded 41 vector(s). 5 unused: declared but imported by no source file, so not indexed; name a package in deps.include to index it anyway
```

Configuration, per workspace:

```yaml
workspaces:
  myapp:
    deps:
      enabled: true                    # default true; false clears the workspace's dependency uses
      include: ["eslint*", zod-to-json-schema]  # index although no source file imports them
      exclude: ["@types/react"]        # never index, do not list
```

`include` takes package names and doublestar patterns on the name. A
declared package that matches is indexed whether or not it is imported
(`include: ["@types/node"]` indexes all of it), and a plain name no
manifest declares is looked up among the transitive packages. `exclude`
wins over both.

- **Go module cache.** codedoc reads `CODEDOC_GOMODCACHE`, else
  `GOMODCACHE`, else `go env GOMODCACHE`, else `~/go/pkg/mod`.
- **Limits.** 600 files and 12 MB per package; a package over them is
  indexed up to the limit and tagged `truncated` in `codedoc deps`.
  Files above 2 MB are skipped, and a file a package links from outside
  its own directory is never read.
- **Local, linked, git and patched packages.** A Go `replace` to a
  directory, an npm `file:`/`link:`/git/URL dependency, a linked
  package and a patched one (pnpm `patchedDependencies`,
  `patch-package`) can change without a new version. Each gets the
  version `local-<hash of its directory>`, is read again when its files
  change (checked by size and modification time on every sync, queries
  included), and is visible only to the workspaces that resolve to that
  directory.
- **npm aliases.** `"lodash": "npm:lodash-es@^4"` is listed as
  `npm/lodash-es@<version> … as:lodash`; imports of `lodash` resolve
  into it.
- **Totals.** Dependencies count in nothing: not in `overview` totals,
  `gaps`, `doc check`, `changes`, `history` or doc coverage, and `doc set`
  refuses a dependency symbol.
- **Self-healing.** If a package a workspace uses is no longer in
  `deps.db` (pruned with `--all`, or the file was deleted), the next
  query notices, indexes it again within its two seconds and reports
  `deps: N pending` for what is left.
- **Upgrades.** Everything in `deps.db` is derived. A package written by
  an older codedoc keeps answering and is extracted again by the next
  `codedoc index`, `codedoc deps index` or daemon catch-up;
  `codedoc deps prune --all` followed by `codedoc deps index` rebuilds it
  from scratch.
