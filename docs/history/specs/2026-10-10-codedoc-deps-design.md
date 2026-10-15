# codedoc: dependency index (addendum to the v1 design)

Status: approved 2026-10-10. Extends `2026-10-09-codedoc-design.md`; where
the two disagree on dependencies, this file wins.

## 1. Problem

A session that builds on third-party packages needs their public API:
what a package exports, the signature of a constructor, which schema to
pass. codedoc never indexes `node_modules/` or the Go module cache, so
agents grep `.d.ts` files and fetch web pages. The trial on a TypeScript monorepo
showed exactly that against the MCP SDK.

## 2. Decisions

| Topic | Decision |
|---|---|
| Scope | Packages the manifests declare directly **and a source file of the workspace imports** (section 4, "Imported packages"). A declared package nothing imports is listed as `unused` and not indexed; `deps.include` indexes it anyway, and transitive packages named there. Whether a manifest lists a package under `dependencies` or `devDependencies` decides nothing. |
| Languages | TypeScript/JavaScript (npm, pnpm, yarn layouts) and Go first. Python and Java later, behind the same interface. |
| Content | Public declarations with their doc comments, and the package README. No resolution inside a package: no call edges, no references. The one exception is the `extends` edge from a type to its supertype, which inherited members are found through (section 5). |
| Store | One shared database, `$CODEDOC_DATA/deps.db`, for every workspace. A package version is indexed once. |
| Version | Identity is `ecosystem/name@version`. A published version is immutable, so its index is never refreshed, only created or pruned. |
| Mutable sources | A package whose content its name and version do not fix (a local directory, a link, a git or URL install, a patched package) is not a published version: its version is `local-<hash of its real directory>`, it is extracted again when its files change, and only the workspaces that resolve to that directory see it (section 4). |
| Visibility | A workspace sees exactly the versions its own manifests resolve to and its code imports, never another workspace's: a version `deps.db` holds for another workspace, or still holds of a package this one stopped importing, is in no answer. |

The shared store is public knowledge, with one exception: a mutable
source may be the user's own unpublished code. Its id carries the hash
of the directory it lives in, so two workspaces share its rows only when
they resolve to the very same directory, which both can read anyway.
What is private (which workspace uses which version, and the edges from
workspace code into a package) stays in the workspace database.

## 3. Identity

- Package id: `<eco>/<name>@<version>`, `eco` being `npm` or `go`:
  `npm/@modelcontextprotocol/sdk@1.20.0`, `go/github.com/spf13/cobra@v1.9.1`.
- In `deps.db` each package version is one `repo` row whose name is the
  package id. Symbols get ordinary stable keys with that repo name, so a
  dependency symbol is addressed as
  `npm/@modelcontextprotocol/sdk@1.20.0:ts:<module>:<qualified>`.
  The stable-key parser must accept `/` and `@` in the repo part; `:` never
  occurs there.
- The repo row of a package version has the package id as its `name` and
  as its `path` (the path column is unique, and one version may be
  installed in many directories), the ecosystem as `vcs` and the version
  as `commit`. Where the package is installed is private to each
  workspace: `dep_use.dir` (section 4). File rows are relative to that
  directory.
- Modules. Every package has a root module at path `.` whose identity is
  the package name (`npm-package`, `go-package`). An npm package adds one
  `npm-export` module per entry point, identified by the import specifier
  (`@modelcontextprotocol/sdk/server`); a Go module one `go-package`
  module per package directory, identified by its import path. The module
  segment of a stable key is the identity of the symbol's module.
- `repo_identity` holds every import specifier the package answers to,
  with the path of the module it names: several specifiers may name one
  module (`…/sdk/server`, `…/sdk/server/index.js`).
- Short address forms work as for workspace symbols (`Server.notification`,
  `path#Symbol`), searched among the workspace's dependency versions when
  `--deps` is given. The Go form `pkg.Name` matches a package by the name
  its files declare (`yaml.Unmarshal` for `gopkg.in/yaml.v3`), which
  extraction stores on the module row (`module.name`, schema version 15),
  and still by the last segment of its import path (`v3.Unmarshal`).

## 4. Discovery

Per workspace repo, on index and whenever a manifest or lock file changes
(tracked by content hash in the workspace database):

Manifests are found among the indexed files of the repo, so those under
an excluded directory (`node_modules/` itself, `vendor/`, exclude globs)
are never read.

**npm.** Every `package.json` in the repo that is not under an excluded
directory. Names come from `dependencies`, `devDependencies`,
`peerDependencies` and `optionalDependencies`. The installed directory is
found the way Node resolves it: `node_modules/<name>` in the manifest's
directory and each parent up to the repo root, symlinks followed (pnpm).
The version is the `version` of the installed `package.json`. A package
whose real path lies inside a workspace repo and outside any
`node_modules` is a workspace package, not a dependency, and so is a
package that is not installed but whose name a workspace repo provides
(spec 7.3 identities); both are recorded as `skipped`. A declared
package that is not installed is recorded as `missing` with its declared
range. When a package ships no types and `@types/<name>` is installed
(`@types/scope__name` for `@scope/name`), that directory supplies the
declarations (`dep_use.types_dir`) and the package supplies the README;
the id stays that of the real package, and the `@types` package is not a
use of its own in that manifest.

**go.** Every `go.mod` in the repo. `require` entries without
`// indirect`. The directory is `$GOMODCACHE/<escaped path>@<version>`
(`CODEDOC_GOMODCACHE`, else `GOMODCACHE` from the environment, else
`go env GOMODCACHE`, else `~/go/pkg/mod`; upper-case letters escape as
`!x`). A `replace` to a local directory uses that directory as a
mutable source (below), unless the directory lies inside a workspace
repo, which is skipped; a `replace` to another module uses that module's
directory and version under the required path. A required module that a
workspace repo provides is skipped, replaced or not. Absent directories
are `missing`; the expected directory is recorded, and discovery runs
again once it exists.

**Aliases (npm).** A package is identified by the `name` of the
installed `package.json`, not by the name the manifest declares it
under: `"lodash": "npm:lodash-es@^4"` is the use `lodash` of the package
`npm/lodash-es@<version>`. `dep_use.name` keeps the declared name, which
is what import specifiers start with, and `dep_use.package` (schema
version 16) the installed one; resolution maps the specifier
(`lodash/fp`) to the package's own (`lodash-es/fp`) before it looks up a
module. `codedoc deps` shows the package id and `as:<declared name>`.

**Mutable sources.** A registry version is immutable; these are not:

- Go: a `replace` to a local directory;
- npm: a declared range that names a directory, a link, a git repository
  or a URL (`file:`, `link:`, `portal:`, `workspace:`, `patch:`, `git+…`,
  `git:`, `git@`, `github:`, `gitlab:`, `bitbucket:`, `gist:`, `http(s):`,
  `ssh:`, a relative or absolute path, the `user/repo` shorthand); a
  package whose real directory lies outside every `node_modules` (a
  link); and a patched package: one named in pnpm's
  `patchedDependencies` (in `package.json` under `pnpm`, or in
  `pnpm-workspace.yaml`, of the manifest's directory or the repo root, by
  `name@version` or by `name`), one with a patch file of `patch-package`
  (`patches/<name>+<version>.patch`, `/` written `+`), or one pnpm
  installed in a directory marked `patch_hash`.

A mutable source gets the version `local-` followed by eight hex digits
of the SHA-256 of its real directory path, so its package id is
`go/example.com/lib@local-3fa9c2d1`. Consequences:

- two workspaces that replace the same module path by different
  directories have different ids and never see each other's code; two
  that resolve to the same directory share its rows;
- every sync, the bounded one of a query included, takes a cheap
  fingerprint of the source (the names, sizes and modification times of
  the files extraction would read: for Go the source files of the
  package directories, `go.mod` and the README; for npm the declaration
  and TypeScript files outside `node_modules` and hidden directories,
  `package.json` and the README; at most 20,000 files) and compares it
  with the one stored with the rows (meta `dep_fingerprint:<id>`). When
  it differs the package is extracted again and the hash of what was
  read (meta `dep_digest:<id>`: modules, docs, specifiers and the content
  hash of every file) compared: the same digest only stores the new
  fingerprint, another one replaces the rows in one transaction and
  queues the files importing the package for resolution (section 6);
- a use recorded with the version `local`, which every replaced module
  shared before, is discovered again by the next sync; the old
  `…@local` rows are left for `codedoc deps prune`.

The result is rows in a new workspace table:

`dep_use(repo_id, manifest_path, eco, name, version, declared, state,
dir, types_dir, error)`, primary key `(repo_id, manifest_path, eco,
name)`, with `state` one of `pending`, `indexed`, `missing`, `failed`,
`skipped`, `unused`. `pending` is an installed version not yet in
`deps.db` (what `deps: N pending` counts); `error` says why a `failed`
one failed; `unused` is an installed package the code does not import
(below); `dir` is where the package is installed and `types_dir` where
its `@types` declarations are. The state column is free text, so
`unused` needed no schema migration. A second table tracks what
discovery read:

`dep_manifest(repo_id, path, size, mtime, hash)`: each manifest, the lock
files next to it and at the repo root (`package-lock.json`,
`npm-shrinkwrap.json`, `pnpm-lock.yaml`, `yarn.lock`, `go.sum`) and the
markers an install writes (`node_modules/.package-lock.json`,
`.modules.yaml`, `.yarn-integrity`), so a fresh install is noticed even
when no lock file changes. A file whose size and mtime are unchanged is
not hashed again. Both tables come with schema version 11.

**Imported packages.** Declaring a package does not get it indexed:
importing it does. A use that is installed (not `missing`, not
`skipped`) is wanted when

- a source file of the repo at or below the manifest's directory has an
  import whose specifier is the declared name or continues it with a
  slash (`@acme/sdk/server` for `@acme/sdk`, a Go package path for its
  module): an `import`, `import type`, `require` or re-export statement,
  a Go import. This is read from the `import` rows of the workspace
  index (`store.ImportSites`: the distinct non-relative specifiers per
  file), whether or not the import resolved, so nothing is parsed again;
  or
- `deps.include` names it or matches it (below), or it is a transitive
  package `deps.include` found.

A use nothing wants gets the state `unused`: it is not extracted, not
embedded, counts in no `deps: N pending` and is in no answer. Whether
the manifest lists the package under `dependencies`, `devDependencies`,
`peerDependencies` or `optionalDependencies` plays no part. The state
is per manifest: the nested package of a monorepo that declares the sdk
without importing it has an `unused` use of the very version the root
manifest's use has `indexed`.

Imports of a **tool configuration file** do not count: a file named
`<tool>.config.<ext>` (`eslint.config.js`, `vite.config.ts`) or a dot
file (`.eslintrc.cjs`), `<ext>` being `js`, `cjs`, `mjs`, `ts`, `cts` or
`mts`. Such a file imports the tools it configures (`@eslint/js`,
`typescript-eslint`), which says nothing about what the code builds on.

**`@types` packages.** `@types/<name>` supplying the declarations of a
declared package that ships none is folded into that package's use
(above) and so follows its imports. An `@types` package that is a use
of its own (the package it declares ships its own types too, or is not
installed, like `@types/estree`) is wanted when the package it declares
is imported: `@types/shapes` when `shapes` is, `@types/scope__name`
when `@scope/name` is. `@types/node` follows the built-in modules of
Node.js (section 5, "Ambient packages").

**Transitions.** Every sync applies the rule, the bounded one of a
query and the daemon's included, after discovery and before indexing:
it reads the imports of each repo once and stores the states that
changed. So when the code starts importing a package its use becomes
`pending` (or `indexed` at once when the workspace already has that
version indexed) and is indexed by the same sync within its deadline;
when the last import goes the use becomes `unused`, the edges into the
package are recomputed (section 6) and no answer shows it any more. The
rows of the package stay in `deps.db`, where another workspace may need
them, until `codedoc deps prune`, which keeps a version only for the
workspaces that have a use of it in another state than `unused`. A
workspace database written by an earlier build, with every declared
package `indexed`, is brought in line by its first sync, a query's
included.

`deps.include` holds package names and doublestar patterns on the name
(`eslint*`, `@types/*`, `github.com/acme/**`). A declared package it
names or matches is indexed although nothing imports it. A plain name
no manifest declares is a transitive package, resolved like a declared
one (npm also looks in pnpm's `node_modules/.pnpm/node_modules`; Go
takes the `// indirect` requirement); it is recorded once per repo, on
the first manifest that resolves it, with an empty `declared`, and one
found nowhere is named in the note of `codedoc deps`. `deps.exclude`
removes names (doublestar patterns on the name) before anything else:
an excluded package is not even listed. `deps.enabled: false` turns the
feature off for a workspace and clears its `dep_use` rows.

## 5. Extraction

Run only when `deps.db` has no repo row for the package id (or, for an
ambient package, a row that lacks a module the workspace imports), and
only for a use that is wanted (section 4): an `unused` package is never
read.

**npm.** Entry points are the subpaths of `exports`, each resolved to
one declaration file: conditions are tried in the order `types`,
`import`, `default`, `require`, then the others by name, nested
conditions and arrays included, and the first target that leads to a
declaration file wins (a `.js`/`.mjs`/`.cjs` target stands for the
`.d.ts`/`.d.mts`/`.d.cts` next to it). One file per subpath keeps the
CommonJS copy of a dual package out. When `exports` has no `.`, the
entry is the `types`/`typings` field, else the declaration file next to
`main`, else `index.d.ts`. A subpath pattern (`./*`) is expanded
statically: every file of the package that one of its targets matches
is a subpath. From each entry, relative `export … from` and `import`
specifiers and `/// <reference path>` directives are followed; a file
belongs to the module of the first entry that reaches it, an entry file
to its own. The `.d.ts` and `.d.cts` twins of one declaration are read
once. Limits: 600 files and 12 MB per package, files above 2 MB skipped;
a package over the limit is indexed up to it and flagged `truncated`
(meta `dep_truncated:<id>`). Subpath exports become modules named after
the subpath (`@modelcontextprotocol/sdk/server/index.js`); when several
subpaths name one file, the module takes the first of: a plain export, a
pattern subpath found through a runtime target, one found through a
`types` target, an ESM specifier before its `.cjs` twin, and the others
stay in `repo_identity`. Symbols: exported declarations (also those
exported by an export clause, `export default` or `export =`, and every
declaration of a file without imports and exports) and their public and
protected members, with signature and TSDoc. Inherited members are not
copied: `Server.notification` of the MCP SDK is `Protocol.notification`,
found through the `extends` edge from `Server` to `Protocol` (see
"Supertypes" below). A package with no declarations at all contributes
only its README.

**Ambient packages.** `@types/node` declares the built-in modules of
Node.js, which no specifier of its own names: `import "node:fs"` and
`import "fs"` read `fs.d.ts` of that package, and its entry file
references every one of its files. Indexing it whole put some 4,000
symbols into a workspace that imports a dozen modules, so it is indexed
lazily:

- the modules wanted are the specifiers the files below the manifest
  import that start with `node:`, or are bare and belong to no declared
  package (`events` is the npm package when a manifest declares it),
  and for which the package has a declaration file `<module>.d.ts`
  (`fs.d.ts`, `fs/promises.d.ts`, `assert/strict.d.ts`);
- extraction starts from those files instead of the entry file and
  follows what they reach through relative imports and reference
  directives, as for any package. The root module with the README is
  kept. What a file imports by module name (`node:stream` from
  `fs.d.ts`) is **not** followed: the names the workspace references
  are declared in the modules it imports, and following the module
  graph would pull in most of the package. The cost is that a supertype
  declared in a module the workspace does not import has no rows, so a
  member inherited from it is not found (`ReadStream.pause` when only
  `fs` is imported);
- `deps.db` records the modules a partial package holds (meta
  `dep_modules:<id>`, a JSON array). The shared rows only grow: a sync
  whose workspace imports a module they lack extracts the union of what
  is held and what it wants, in one transaction, and a workspace that
  names the package in `deps.include` extracts it whole (the key then
  holds `*`). Every sync checks this for an indexed ambient use (a few
  meta lookups), as it checks the fingerprint of a mutable source;
- an `@types/node` an earlier build indexed whole has no such key: the
  first sync of a workspace that uses it, a query's included, replaces
  its rows by the modules that workspace imports (its vectors are
  embedded again);
- with no built-in module imported the use is `unused`.

`codedoc deps` shows `modules:N` on such a package and the list in
JSON. A workspace may see modules another workspace added to the shared
rows; they are files of a package it uses. Imports of built-in modules
get no edges, as before: the specifier `node:fs` names no module row.

**go.** Every non-test `.go` file outside `internal/`, `testdata/` and
`vendor/`, directories starting with `.` or `_`, nested modules and
`main` packages. Exported identifiers and exported methods and fields of
exported types, with signature and doc comment; package doc as the
module doc. The npm limits apply, directories being read shallowest
first. A name declared in several files under build constraints gets the
path-hash suffix of spec 5.1 on each.

Both: the README at the package root becomes the doc of the root module,
whole up to 32 KB (for a Go module it wins over the root package's doc).

**Containment and caps.** A package is untrusted input and may link its
files anywhere. Before a file of a package is read (a declaration or
source file, the file scanned for reference directives, the README,
`package.json`), its path and the package directory are resolved through
symbolic links, and a file that does not lie inside the package's real
directory is skipped: a linked declaration contributes no symbols, a
linked README no doc, and a package whose `package.json` is such a link
is `missing`. Nothing large is read whole: a file is stat'ed first; a
declaration or source file above 2 MB is skipped, a `package.json`,
`go.mod` or `pnpm-workspace.yaml` above 4 MB is an error, and at most
32 KB plus one byte of a README are read.
No bodies are stored (a symbol row keeps its line range, so
`symbol --body` reads the file from disk when it still exists). No
references are extracted, and no edges but the supertype ones. The
existing extractors are reused with a mode that keeps exported
declarations only.

**Supertypes.** The API mode of the TypeScript extractor also returns
the types the `extends` clauses of the kept classes and interfaces name
(`implements` is not needed: a class declares the members it
implements). A Go struct's supertypes are the types it embeds: a field
named after its own type (`Base`, `*Base`); a type of another package
(`sync.Mutex`) is not followed. When a package is written, each such
name is resolved to one declaration of the same package and stored as an
`extends` edge in `deps.db` (`reference_id` NULL), in this order: a
declaration next to the subtype (same file, same container), the one in
the file the name is imported from, one in the subtype's module, else
the only one in the package; a qualified name (`ns.Base`) must end the
declaration's qualified name. A name that leaves several candidates, or
names a type of another package (`EventEmitter` of `node:events`), gets
no edge: inheritance across packages is not followed.

**Extraction version.** `deps.db` records per package version the
version of the extraction that wrote it (meta `dep_extraction:<id>`;
version 1 wrote none and no supertype edges, version 2 no re-export
graph (section 6), this is version 3). A
package written by another version keeps answering queries and is
extracted again, its rows replaced in one transaction, by the next full
sync: `codedoc index`, `codedoc deps index` or the daemon's catch-up. A
query's bounded sync leaves it. Nothing else is needed to upgrade an
existing `deps.db`; `codedoc deps prune --all` followed by
`codedoc deps index` rebuilds it from scratch.

Extraction of a package happens in one transaction; a failed package
leaves no rows and is recorded as `failed` with the error.

## 6. Edges from workspace code

Resolution's import tier already knows the import specifier of an
unresolved reference. When the specifier belongs to a dependency of the
referencing file, the reference resolves to the dependency symbol:

- the edge is stored in the workspace database with `dst_id NULL` and a
  new column `dst_dep_key` (schema version 12) holding the dependency
  symbol's stable key, confidence `import`;
- `callees` lists such targets tagged `[dep]`, with the signature and
  doc of the dependency symbol when `deps.db` still holds it, once per
  caller and target;
- `callers <dependency symbol>` and `refs <dependency symbol>` look edges
  up by `dst_dep_key`, answering "where do we use this API";
- when the workspace moves to another version, edges are recomputed for
  the files importing that package (the manifest change triggers it).

**When.** The dependency tier runs last, for a reference the workspace
tiers left unresolved, so it costs resolved references nothing and a
workspace symbol always wins. A workspace without indexed dependency
uses pays one small query per resolution run and never opens `deps.db`.
The resolver finds `deps.db` through the path the last dependency sync
recorded in the workspace database (meta `deps_db`).

**Which package.** The package is the one a manifest of the file's repo
resolves the specifier's package to: the nearest manifest that declares
it, walking up from the file's directory (the spec first said "the
nearest manifest"; a file of a nested package may import what only the
root manifest declares, as Node resolves it). For npm the package is the
first segment of a bare specifier, two for a scope; for Go the longest
prefix of the import path that is a required module.

**What resolves.**

- A name a TypeScript import brings in (`import { Server }`, used as a
  type, with `new`, or called; `import { Server as S }` binds `S` to
  `Server`) and `ns.Name` through a namespace or default import: the
  top-level declaration of that name in a file the specifier exports it
  from (its entry file and, transitively, the files that one re-exports
  with `export * from`; a named re-export `export { a, b as c } from`
  leads to its source and source name), nearest first, else the one in
  the module the specifier names, else the only one in the package.
- A member of a namespace the package exports, reached through a named
  import: zod's `import { z } from 'zod'; z.object(...)`, where the
  entry binds `z` with `import * as z from './v4/classic/external.js';
  export { z }` or `export * as z from ...`. The member is looked up in
  the files of that namespace the same way. The re-export graph the
  lookups walk is stored per package version in `deps.db` (meta
  `dep_exports:<id>`, JSON: files, entry file per specifier, star
  re-exports, named re-exports and namespace bindings per file); a
  package written before extraction version 3 has none and resolves as
  before until it is extracted again.
- `Class.member` through a named TypeScript import: the member of that
  class, or the one it inherits. The edge carries the key of the symbol
  that declares the member, so `callers Server.revision` and
  `callers Protocol.revision` list the same uses.
- `pkg.Name` in Go: the declaration of the package the import path
  names, exactly.
- A member reached through a field of the enclosing type whose declared
  type is a dependency type: `a.client.Do()` with `client *kit.Client`
  in Go, `this.server.notification()` with `private server: Server` in
  TypeScript; inherited (embedded) members included.
- A member reached through a local variable, a parameter or (TS) a class
  property whose declaration names a dependency type (v1 spec 7.3, local
  variables): `const server = new Server(); server.setRequestHandler()`
  and `(client: Client) => client.connect()` in TypeScript,
  `c := kit.New(); c.Do()`, `var c kit.Client` and `func run(c
  *kit.Client)` in Go. For `kit.New()` the type is the first result of
  the function's signature in `deps.db`, when it names a type of the
  same package.

**What does not.** `this.member()` in a workspace class that extends a
dependency class is not linked, nor is a chain through a member of a
dependency type (`pkg.Var.Method()`, `c.Field.Method()`), nor a variable
whose type no declaration states (`const s = makeServer()` in
TypeScript, the result of a method call). A name several declarations of the package could
be is left unresolved: an edge into a dependency has one target.

**Recomputation.** A dependency sync compares the indexed version every
manifest resolves each package to before and after it. For each package
that moved (another version, a version that was pending and is now
indexed, a package no longer used) and each package the sync wrote to
`deps.db` (also one an older extraction had written), the files of the
repo with an unresolved import of it are queued and resolved like any
pending resolution. The first sync of a workspace against a `deps.db`
(no `deps_db` recorded yet, or another path) does so for every package:
that gives a workspace indexed before schema version 12 its edges
without a rebuild. `codedoc index` syncs the dependencies before it
resolves, so a first index or a rebuild resolves once.

## 7. Queries

- `find`, `outline`, `symbol`, `search`, `explain` take `--deps`:
  dependency results are included, tagged `[dep name@version]` (in a
  list they end, once per run of entries of one package; `find --deps`
  lists them by package as name, location and signature, the doc
  sentence with `--detail` or `--doc`). Without
  the flag the dependencies are searched second: a name no workspace
  symbol has is looked up in them (`symbol Server`, `find Server`,
  `outline Server`, `explain Server.notification`), so `--deps` is
  needed only to list a dependency symbol next to a workspace symbol of
  the same name, and for `search` (efficiency addendum, section 5). An
  address that names a dependency needs neither: one
  that starts with a package id, or a path inside a dependency directory
  (`repo:path`, absolute, or relative to the working directory or the
  repo; symbolic links followed), is resolved in `deps.db` directly. One
  resolver serves every command that takes such a file, so `outline
  <file>`, `symbol <file>#Name`, `symbol <file>:LINE` and `lines <file>
  A-B` accept the same forms: `lines` prints the range under the
  dependency symbols on it (its header line ends with `[dep
  name@version]`), and `symbol <file>:LINE` on a line no symbol covers answers as
  for a workspace file: the symbols of the file, with the note `no
  symbol encloses line 1 of dist/x.d.ts (15 lines)` (efficiency
  addendum, section 4.1), not with `no indexed file`. A file in the
  directory of a package that is not in `deps.db` says why in one line
  (`... is not indexed: no source file imports the dependency
  typescript; name it under deps.include in the config to index it`). `outline` and
  `explain` also take a package name without the flag (next item): the
  name is such an address, and it is tried only when the workspace has
  no path or module of that name.
- `outline <package name>` lists the modules of the package (also
  `name@version` or the package id; modules without declarations and doc
  are counted in the note); `outline <package name>/<subpath>` the
  symbols of one module, members folded into `+N members`.
  `outline <path in a package directory>` lists the symbols of that
  file. These outlines page as the workspace ones do (`--offset`, the
  same section and folding code), and an answer from the dependency
  index carries no note of the workspace attempt that preceded it: an
  `--offset` inside the dependency outline is not reported as past the
  end of a workspace directory that does not exist. `outline <type>` of a dependency class, interface or struct (a
  stable key, or a name no workspace path, module or symbol has) lists
  its members, then under the
  heading `inherited members` the public members its supertypes declare,
  nearest first, each tagged `[declared on <type>]`, at most 30; the note
  counts the rest. `explain <package name>` gives the lead of the README
  and the symbols of the package the workspace uses most (by edges),
  `explain <package name>/<subpath>` the same for one module.
- Inherited members: `symbol Server.notification --deps` and
  `find Server.notification --deps` resolve through the `extends` edges
  of `deps.db` when no symbol has that qualified name, answer with the
  declaring symbol and tag it `[declared on Protocol]`.
- `symbol` of a dependency prints the absolute location in the directory
  the package is installed in and the tag `[dep name@version]`; with
  `--detail` also `[kind] [container:…] [module:…] [declared on …]
  [fan-in:N]` (N counts the edges of the workspace that end at it), and
  no doc state. The source, printed by default, is read
  from that directory (`DepUse.Root()`, the current repo's
  first). When no directory of the package is left the note says
  `body unavailable: <id> is no longer installed (<dir>)`; when the file
  differs from the indexed one, that it differs.
- An address that matches several symbols is answered for the best
  candidate, a workspace symbol before a dependency's, the others named
  in the `also:` line; with `--strict` the workspace and the dependency
  candidates are listed together and it is ambiguous (exit 3).
- Two versions of one package in a workspace: both are searched and every
  entry carries its version; `--repo` narrows to the version that repo
  uses.
- `callers` and `refs` take a dependency symbol without a flag: a name
  no workspace symbol has is looked up in the dependencies (section 6).
  `--depth` applies to workspace symbols; for a dependency symbol the
  direct uses are listed and the note says so. Every other command that
  takes a symbol works on workspace symbols only: given a stable key or
  a path of a dependency it fails with "is a dependency symbol"
  (exit 1); a bare name is not looked up in the dependencies: it is
  answered with the nearest workspace names, or not found.
- Dependencies never count in `overview` totals, `gaps`, `doc check`,
  `changes`, `history`, or doc coverage. `overview` gains one section,
  `dependencies (N)`, before the module tree: package id, state, symbol
  count, per repo; workspace packages (`skipped`) and packages the code
  does not import (`unused`) are left out, and a workspace without
  dependencies has no such section.
- `doc set` refuses a dependency symbol.
- An answer that looked at dependencies while package versions wait to
  be indexed ends its note with `deps: N pending`.
- `codedoc deps [--repo R]`: the `dep_use` table with states and errors,
  the `unused` packages included (`npm/eslint@10.12.0  package.json
  unused declared:^10`, in JSON `"state":"unused"`); when there are any
  the note ends with `N unused: declared but imported by no source
  file, so not indexed; name a package in deps.include to index it
  anyway`. There is no `codedoc deps index <package>`: the states are
  derived from the code and the config at every sync, so a package
  indexed by hand would be unused again at the next one. A partially
  indexed ambient package shows `modules:N`. The line `codedoc index`
  prints per repo counts `indexed:N pending:N missing:N failed:N
  skipped:N unused:N`.
  `codedoc deps prune`: deletes from `deps.db` every package version no
  workspace uses, as section 9 defines it (`--dry-run` lists them and
  deletes nothing, `--all` empties `deps.db`). `codedoc deps index`:
  discover and index now. `deps index` and a `deps prune` that deletes
  print nothing on success (efficiency addendum, section 6):
  `--verbose` prints the listing, respectively what was deleted,
  `--format json` the result object; `deps prune --dry-run` always
  prints its list. A silent `deps index` (and `index`) whose sync left
  failed package versions or unreadable manifests warns in one line on
  stderr. The v1 design already has `codedoc deps <module>` (the modules a
  module imports), so the command is shared: with a module argument it
  is the import query, without one it lists the packages, and `index`
  and `prune` are its subcommands (a module literally named `index` or
  `prune` is addressed as `repo:index`). The listing runs the same
  bounded sync as a query; the listing, `index` and `prune` take
  `--budget`, `--offset` and `--out`.
- MCP: the find, outline, symbol, search and explain tools gain a `deps`
  boolean; a new `codedoc_deps` tool covers `codedoc deps` with an
  `action` of `list` (the default), `index` or `prune` (`all` for
  `prune --all`, `dry_run` for `prune --dry-run`). With the tools of the documents addendum the server
  has fifteen tools. (The efficiency addendum, section 9.1, later
  dropped `deps` from outline and explain, and `all` and `dry_run`.)
- `codedoc api <package> [name]` (efficiency addendum, section 5.1)
  answers for a dependency without `--deps`: the package by the name a
  manifest declares (`zod`, `@scope/name`, a Go module path, optionally
  `name@version`) or one of its modules by its import specifier; with a
  name, a type with its members and the inherited ones (the `extends`
  edges of deps.db, as `outline <Type> --deps` follows them), a
  function, or `Type.member`. It reads the package versions the
  workspace resolves to and nothing else, as every query does (section
  2). A package in a state other than `indexed` (section 4: `unused`,
  `missing`, `pending`, `failed`, `skipped`) answers with one line that
  says the state and what to do, exit 0; with two versions of one name
  the first in id order is answered and the other is named in the note
  (`codedoc api name@version` picks one). MCP: `codedoc_api`.

## 8. Embeddings

`deps.db` has the same vector tables. Documented dependency symbols and
module docs (README included) are embedded once per package version,
outside the write lock, never while `codedoc index` runs.
`search --deps` fuses workspace and dependency lists with the existing
reciprocal rank fusion.

- The text of a dependency symbol is its path, container, signature and
  doc; there is no body to add, and no file is read, so a package whose
  directory is gone is embedded all the same. A doc longer than 1,200
  bytes is embedded one vector per passage, as in the workspace
  (efficiency addendum, section 3), split by the lines of the stored doc
  text. Undocumented dependency symbols are not embedded.
- **When.** Embedding the dependencies of the trial repository took most of a
  three-minute `codedoc index` for code that is not the user's, so
  `codedoc index` embeds the workspace only. Dependency vectors are
  written in the background by the daemon, after the workspace vectors
  of a repo are current and its dependencies are synced, and on request
  by `codedoc deps index --embed` (`embedded N vector(s)` in its note).
  Only indexed packages have anything to embed, and since the indexing
  policy of section 4 those are the ones the code imports. The pass
  covers the package versions the repos at hand use and writes in small
  transactions without `deps.lock`.
- **Order.** The packages a manifest declares for running the code are
  embedded before those it declares for development only
  (`devDependencies` and nowhere else; `dep_use.dev`, schema 21): an
  interrupted pass leaves the vectors that matter most. Go modules have
  no such distinction and count as runtime.
- **State.** `codedoc deps` shows `vectors:N/M` per indexed package: its
  documented symbols and modules with a current vector over all of them
  (JSON `extra.vectors: {embedded, total}`, and `extra.dev`).
- `search --deps` and `where --deps` catch up at most 64 missing
  dependency vectors before answering, like the workspace's; above that
  the note says `N dependency vectors are missing: dependencies are
  matched by keywords only; run codedoc deps index --embed, or let the
  daemon embed them`.
- The fused lists are the workspace's symbol and module lists (text and
  cosine) and the same four of the packages the query sees; on a tie a
  workspace hit comes first. `--kind` and `--lang` apply to both.

## 9. Freshness, locking, daemon

- `deps.db` has its own write lock (`deps.lock`), taken only while a
  package is being written; workspace locks are never held meanwhile.
  Packages are extracted on a few goroutines and written one at a time.
- A query's freshness check runs discovery again only when a manifest or
  lock file hash changed. It always applies the import rule of section 4
  to the uses it has (one read of the repo's distinct non-relative
  import specifiers, none for a repo without uses), since any edited
  source file may have added or removed an import. Indexing missing
  packages on the query path is
  bounded to 2 s (no package is started after it; one that was started
  finishes); what is left is reported in the frame as
  `deps: N pending` and picked up by `codedoc index`, `codedoc deps index`
  or the daemon.
- **Vanished packages.** `dep_use.state` is a cache of what `deps.db`
  holds, and `deps.db` can lose a package behind a workspace's back
  (`prune --all`, a deleted file, a prune by an older build). Every
  sync, the bounded one of a query included, therefore checks that the
  package version of each `indexed` use of the repos it covers has its
  row in `deps.db` (one lookup per version); a use whose package is
  gone is set back to `pending`, indexed again within the same deadline,
  and otherwise counted in `deps: N pending`, so a `--deps` query never
  answers empty in silence and the next one heals. The sync also checks
  the fingerprint of every mutable source (section 4).
- **Users and prune.** `deps.db` records which workspace databases use
  each package version: `dep_user(package, workspace_db, seen_at)`
  (schema version 17; indexed by `workspace_db` since 18), the absolute
  path of the workspace database and when a sync last wrote the row. A sync replaces the rows of its
  workspace when the set of versions it resolves to (in any state but
  `unused`) changed, and a full sync renews `seen_at`; this is the only thing a
  workspace writes into `deps.db` about itself, under `deps.lock`.
  `codedoc deps prune` can only read the config it runs under, so it
  does not decide by that config alone. Under `deps.lock`, so that no
  sync indexes a package between the decision and the deletion, it
  computes the versions to keep as the union of: the uses of every
  workspace of the config; and, for every workspace database recorded
  in `dep_user`, its current uses, read from that database (the
  recorded rows when it cannot be opened). A recorded database whose
  file no longer exists is forgotten. Everything else is deleted with
  its meta keys; the answer lists the deleted ids and names the
  workspace databases whose packages were kept and those forgotten.
  `--dry-run` lists and deletes nothing. A `deps.db` written before
  schema version 17 has no users until each workspace syncs once; what
  a prune takes from such a workspace in the meantime comes back
  through the vanished-package check. A `failed` package is retried by those three, not by
  queries; they also check that the `indexed` versions are still in
  `deps.db`; those three also extract again a package an older
  extraction version wrote (section 5). A `deps.db` that cannot be opened
  is a note on the answer, never an error of the query.
- The daemon watches manifests and lock files like any file and indexes
  new versions in the background, also at its catch-up, after a HEAD
  move and after a rescan. It runs the bounded sync after every batch of
  changed paths, not only after a manifest change: a source file may
  import a package for the first time. A manifest or lock file that an exclude glob
  or `.gitignore` keeps out of the index still triggers discovery.
- `codedoc index --rebuild` rebuilds the workspace database and keeps
  `deps.db`; `codedoc deps prune --all` empties it and marks the indexed
  uses of every configured workspace `pending` (a workspace of another
  config notices on its next sync).

## 10. Configuration

```yaml
workspaces:
  web-app:
    repos:
      - path: ~/src/web-app
    deps:
      enabled: true          # default true
      include: ["eslint*", zod-to-json-schema]   # index although no source file imports them
      exclude: ["@types/react"]                  # never index, do not list
```

Without the section every declared package the code imports is indexed
and the rest is `unused` (section 4). `include` takes package names and
doublestar patterns on the name: a declared package that matches is
indexed whether or not it is imported (a whole `@types/node` with
`include: ["@types/node"]`), and a plain name no manifest declares is
looked up among the transitive packages. `exclude` wins over both. An
empty name or a malformed pattern in either list fails the config load.

No workspace may be named `deps`: its database would be `deps.db`.
`CODEDOC_GOMODCACHE` overrides the Go module cache codedoc reads; tests
point it at a fixture and the test isolation at an empty directory, so
the real cache is never read.

## 11. Testing

Fixtures under `testdata/ws` gain a small fake `node_modules` (one scoped
package with `exports` types conditions, one pnpm-style symlinked package,
one `@types` pair, one package installed at two versions, a dev tool
`lint-tool` no file imports, and an `@types/node` with five modules of
which `src/files.ts` imports two, `node:fs` and `path`) and a fake Go
module cache selected with `CODEDOC_GOMODCACHE`: the repos `deps-app` and
`deps-go`, the cache `gomodcache` and the locally replaced module
`golocal`. The scoped package has a class that extends another
(`Server extends Protocol`), the Go module a struct that embeds one
(`Client` embeds `Base`), and the two repos use them through imports,
fields and inherited members. Three declared packages are imported by
no file and so `unused`: `lint-tool`, `plain-js` (the `@types` pair; the
tests that read it name it in `deps.include` or add an import) and
`github.com/BurntSushi/toml` (required by `go.mod` only); the nested
package `packages/web` declares the sdk and imports only `dup`. `testdata/ws/config.yaml` lists the two
repos as the workspace `fixture-deps`, which the README examples run on.

Integration (`make docker-integration`) runs against a real `pnpm
install` and a real `go mod download` of small pinned packages:
`testdata/realdeps` holds `npm-app` (`@noble/hashes` 1.4.0, a scoped
package with an `exports` map whose `HMAC` class extends a class of
another file, and `ms` 2.1.3 with `@types/ms` 2.1.0, an `@types` pair)
and `go-app` (`github.com/jinzhu/now` v1.1.5, whose `Now` struct embeds
`Config`, and `github.com/pkg/errors` v0.9.1), each with its lockfile
(`pnpm-lock.yaml`, `go.sum`). The integration image installs them when
it is built (`pnpm install --frozen-lockfile`; `go mod download` into a
module cache of its own, `/opt/realdeps/gomodcache`, which the tests
select with `CODEDOC_GOMODCACHE`) and names the result in
`CODEDOC_REALDEPS`, so the tests fetch nothing when they run. Both
fixture repos import every package they declare, so all four versions
are indexed and the count lines end with `unused:0`. They
assert the discovery states (`index`, `deps`, `overview`), symbols with
`--deps`, `outline` of a package and of a class or struct with its
inherited members, and `callers` and `callees` across the boundary.
Without `CODEDOC_REALDEPS` (`make integration` on a host) they are
skipped; with it, a missing install fails them.

## 12. Later

Python (site-packages and stubs), Java (sources jars), a published shared
`deps.db` that machines can download instead of indexing locally.
