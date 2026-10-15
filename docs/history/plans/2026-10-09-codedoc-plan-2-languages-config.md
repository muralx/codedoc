# codedoc Plan 2: Python and Java extractors, config keys

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Index Python and Java code with the same depth as Go and TypeScript, index configuration and infrastructure files as config keys, link code that reads a key to the files that define it, and answer `codedoc config`.

**Architecture:** Two new pure extractors register in `internal/extract` like the Go and TS ones. A new package `internal/extract/cfg` holds one pure extractor per config format and returns key definitions and key references. The pipeline stores config keys and uses; the resolver matches `RefConfig` candidates from code against key rows workspace-wide. Spec: sections 5.1, 7.2, 7.3, 8, 9 (`config`), 17 steps 4 and 5.

**Tech Stack:** `github.com/tree-sitter/tree-sitter-python` v0.25.0, `github.com/tree-sitter/tree-sitter-java` v0.23.5, `github.com/tree-sitter/tree-sitter-bash` v0.25.1, `gopkg.in/yaml.v3` (node API for line numbers), `github.com/hashicorp/hcl/v2` (hclsyntax), `encoding/json` (decoder with `InputOffset`), `encoding/xml` (decoder with `InputOffset`).

**Conventions:** identical to Plan 1 (test first, small files and functions, doc comments, `make test` and `make lint` green, attribution line on every commit). Fixtures stay plain files under `testdata/ws/`; tests use `testutil.CopyFixture`.

---

## File structure

```
internal/extract/python/python.go, walk.go, refs.go, python_test.go
internal/extract/java/java.go, walk.go, refs.go, java_test.go
internal/extract/configref.go          shared helpers: key-like literal test, accessor tables
internal/extract/cfg/types.go          ConfigResult, KeyDef, KeyRef
internal/extract/cfg/registry.go       format detection by path, registry
internal/extract/cfg/yaml.go, json.go, xml.go, properties.go, env.go, hcl.go, shell.go, template.go
internal/extract/cfg/*_test.go
internal/resolve/identity_python.go, identity_java.go (extend the identity map)
internal/resolve/config.go             config candidate matching
internal/store/config.go               config_key and config_use rows, queries
internal/query/config.go               the config query
testdata/ws/py-lib/                    python fixture
testdata/ws/java-lib/                  java fixture (Maven layout)
testdata/ws/cfg-repo/                  config fixture
```

Golden files go under `internal/extract/golden/<fixture>/...json` as in Plan 1.

---

### Task 1: Python fixture and extractor (symbols, docs, tests)

**Fixture** `testdata/ws/py-lib/`:

- `pyproject.toml`: `[project]\nname = "fixture-pylib"\n` and `[tool.setuptools.packages.find]\nwhere = ["src"]`.
- `src/pylib/__init__.py`: module docstring `"""Fixture library for Python."""` and `from .store import MemStore, Store`.
- `src/pylib/store.py`: module docstring; `class Store(abc.ABC)` with docstring and abstract methods `get(self, key)` and `put(self, key, value)` with docstrings; `class MemStore(Store)` with docstring, `__init__`, `get` (docstring, raises `KeyError` via `_missing(key)`), `put` (no docstring); module-level function `_missing(key)`; module constant `DEFAULT_TIMEOUT = 30`; a call `os.environ.get("PYLIB_MODE", "dev")` inside a function `mode()`.
- `src/pylib/service.py`: `from pylib.store import Store` and `class Service` with `lookup(self, key)` calling `self.store.get(key)` and `fallback(key)`; module function `fallback(key)`.
- `tests/test_store.py`: `from pylib.store import MemStore` and `def test_put_get():` calling `MemStore()`, `.put`, `.get`.

**Extractor rules** (tree-sitter-python v0.25 node kinds):

| Node | Symbol | Notes |
|---|---|---|
| `function_definition` (also inside `decorated_definition`) at module level | function | `async` keyword present makes no difference to kind |
| `function_definition` inside a `class_definition` body | method | Container = class |
| `class_definition` | class | `superclasses` field is an `argument_list`; each identifier or attribute in it is a `RefExtend` reference |
| `expression_statement` > `assignment` at module level whose `left` is an `identifier` | const when the name is ALL_CAPS, else var | |
| `assignment` to `self.<name>` inside `__init__` | field | Container = class, first occurrence only |

- Qualified name: dotted inside the file (`MemStore.get`); the module identity is the file's dotted module path (spec 5.1), computed by the pipeline, so the extractor emits names relative to the file.
- Visibility: name starting with `__` and not ending with `__` is `Private`; starting with `_` is `Package`; else `Public`.
- Doc: the first statement of the body when it is an `expression_statement` holding a `string`; strip the quotes and common indentation (`inspect.cleandoc` semantics). Module doc: the first statement of the module, same rule.
- Signature span: from the `def`/`class` line to the line of the `:` that starts the body. SigText collapses whitespace and excludes the body.
- Body hash: whitespace-stripped body text excluding the docstring statement, so editing a docstring never changes the body hash.
- Tests: file `test_*.py`, `*_test.py`, or under a `tests/` directory; functions named `test_*` and methods named `test_*` of classes named `Test*` are test symbols.

- [ ] Write goldens harness (as Plan 1 Task 9, over `testdata/ws/py-*`), expecting the symbols above with correct lines, docs, visibility and test flags.
- [ ] Run, expect failure. Implement `python.go` (builders), `walk.go` (walker). Register for `python` (extension `.py`, family `py`).
- [ ] Add `python` to `parse.LanguageForPath` and `parse.Family` (`py`), and the grammar to the registry, with tests.
- [ ] Update goldens, review by hand, pass, lint, commit `feat(extract/python): symbols, docs and tests`.

### Task 2: Python references, imports, config candidates

| Node | Reference / import |
|---|---|
| `call` with `function` = `identifier` | `RefCall`, Arity = argument count (keyword arguments count) |
| `call` with `function` = `attribute` | `RefCall`, Name = `attribute` field, Qualifier = `object` text |
| `call` whose callee name starts with an upper-case letter | additionally `RefInstantiate` with the same name (Python has no `new`) |
| `type` annotations (`identifier` inside a `type` node) | `RefType` |
| `attribute` not in call position | `RefField` |
| `import_statement` (`import a.b as c`) | `Import{Raw: "a.b", Alias: "c" or ""}` |
| `import_from_statement` (`from a.b import x as y`) | `Import{Raw: "a.b", Names: ["x"]}`; aliases recorded as `x=y`; relative imports keep their dots (`.store`) |

Config candidates (`RefConfig`, see spec 7.2): `os.environ["X"]`, `os.environ.get("X")`, `os.getenv("X")`, and any string literal argument of a call matching the key-like patterns. Put the key-like regular expressions and the per-language accessor tables in `internal/extract/configref.go` so all extractors share them:

```go
// LooksLikeKey reports whether a string literal could name a config key:
// env style (FOO_BAR, at least three characters) or dotted lower case (a.b.c).
func LooksLikeKey(s string) bool
```

- [ ] Extend goldens: `Service.lookup` calls `get` with Qualifier `self.store` and `fallback` bare; `mode` has a `RefConfig` named `PYLIB_MODE`; `test_put_get` instantiates `MemStore`.
- [ ] Implement `refs.go`; pass; lint; commit `feat(extract/python): references, imports, config candidates`.

### Task 3: Java fixture and extractor

**Fixture** `testdata/ws/java-lib/` (Maven layout):

- `pom.xml` with `<groupId>com.fixture</groupId><artifactId>javalib</artifactId>` and a `<properties><lib.mode>dev</lib.mode></properties>` block.
- `src/main/java/com/fixture/store/package-info.java`: Javadoc `/** Storage abstractions of the Java fixture. */ package com.fixture.store;`
- `src/main/java/com/fixture/store/Store.java`: `public interface Store` with Javadoc and methods `String get(String key)` and `void put(String key, String value)` with Javadoc.
- `src/main/java/com/fixture/store/MemStore.java`: `public class MemStore implements Store` with Javadoc, a private field `data`, a constructor, `get` (Javadoc), `put` (no Javadoc), and an overload `put(String key, int value)` that calls `put(key, String.valueOf(value))`.
- `src/main/java/com/fixture/service/Service.java`: imports `com.fixture.store.Store`; class `Service` with a field `store`, a constructor taking `Store`, and `lookup(String key)` calling `store.get(key)` and `fallback(key)`; a private static `fallback`; a field annotated `@Value("${lib.mode}")`; a call `System.getenv("JAVALIB_HOME")`.
- `src/test/java/com/fixture/store/MemStoreTest.java`: class with `@Test void putThenGet()` calling `new MemStore()`, `put`, `get`.

**Extractor rules** (tree-sitter-java v0.23):

| Node | Symbol | Notes |
|---|---|---|
| `class_declaration`, `record_declaration` | class | nested classes qualify as `Outer.Inner` |
| `interface_declaration`, `annotation_type_declaration` | interface | |
| `enum_declaration` | enum | enum constants become `const` symbols |
| `method_declaration` | method | always carries a signature hash: first 4 hex of xxhash of the normalized parameter type list (`String,int`), spec 5.1 |
| `constructor_declaration` | method named `<init>` | signature hash as for methods |
| `field_declaration` > `variable_declarator` | field (const when `static final`) | |

- Visibility: `public` is `Public`, `private` is `Private`, `protected` and package-private are `Package`. Interface members are `Public`.
- Doc: a `block_comment` starting with `/**` immediately preceding the declaration (modifiers and annotations belong to the declaration). Strip `/**`, `*/`, and leading `*`. Module doc: the Javadoc on the `package_declaration` in `package-info.java`.
- Tests: files under `src/test/`; methods annotated `@Test`, `@ParameterizedTest`, or `@RepeatedTest` are test symbols.
- `ModuleName`: the package name from `package_declaration`.

The extractor needs to expose the signature hash on the symbol; add a field `SigHash string` to `extract.Symbol` (empty for other languages), and make the pipeline use it in the stable key instead of computing one.

- [ ] Goldens for `testdata/ws/java-*`; expect both `put` overloads with distinct `SigHash`, the constructor as `MemStore.<init>`, field `data` private, `putThenGet` a test.
- [ ] Implement, register for `java` (family `java`), parser registry and tests; pass; lint; commit `feat(extract/java): symbols, overload hashes, docs and tests`.

### Task 4: Java references, imports, config candidates

| Node | Reference / import |
|---|---|
| `method_invocation` | `RefCall`, Name = `name`, Qualifier = `object` text or "" |
| `object_creation_expression` | `RefInstantiate`, Name = `type` text without generics |
| `type_identifier` in declarations, parameters, generics | `RefType` (skip `String`, boxed primitives, `Object`, `void`) |
| `field_access` | `RefField` |
| `superclass` | `RefExtend` |
| `super_interfaces` > `type_list` children | `RefImplement` |
| `import_declaration` | `Import{Raw: "com.fixture.store.Store", Alias: "Store"}`; wildcard imports keep `Raw: "com.fixture.store.*"` and no alias |

Config candidates: `System.getenv("X")`, `System.getProperty("x")`, `@Value("${x}")` and `@Value("${x:default}")` (the key is `x`), `env.getProperty("x")`, plus key-like literal arguments.

- [ ] Extend goldens; implement; pass; lint; commit `feat(extract/java): references, imports, config candidates`.

### Task 5: Config candidates in the Go and TypeScript extractors

Go: `os.Getenv("X")`, `os.LookupEnv("X")`, `viper.Get*("a.b")` (any method whose name starts with `Get` on a qualifier `viper`), plus key-like literal arguments of any call. TypeScript: `process.env.X`, `process.env["X"]`, plus key-like literal arguments.

Add to the fixtures: `go-lib/service/service.go` gains `func Mode() string { return os.Getenv("LIB_MODE") }`; `ts-domain/src/records.ts` gains `export const apiUrl = () => process.env.API_URL;`. Update every golden and any Plan 1 test whose line numbers or symbol counts move (search the tests for the affected files and adjust; report each change).

- [ ] Implement and test; commit `feat(extract): config key candidates in go and typescript`.

### Task 6: Python and Java module identities and import resolution

Identity map additions (spec 5.1, 7.3):

- Python: the package root is the nearest directory upward holding `pyproject.toml` or `setup.py`; when it has a `src/` child, the root is `src/`. Every `.py` file is a module with identity = dotted path relative to the root (`pylib.store`; `__init__.py` maps to the package `pylib`); every package directory is a module too, and file modules have the package module as `parent_id`. The repo identity comes from `[project] name` in `pyproject.toml`. Files outside any root (for example `tests/`) are modules named by their dotted repo-relative path.
- Java: modules are packages (`com.fixture.store`), identity written with dots (no colon issue). The repo identity is `groupId/artifactId` from `pom.xml` (spec 5.1: written with `/` in keys) or the `rootProject.name` from `settings.gradle(.kts)`.

Import resolution: Python absolute imports match module identities exactly or by prefix (`pylib.store` imports the module; `from pylib import store` resolves `pylib` and records `store` as a name that may be a submodule); relative imports resolve against the importing file's package. Java imports `a.b.C` resolve to module `a.b` and record `C` as a name; wildcard imports resolve to the module.

Resolution tier `import` for these languages uses `Names` the way TypeScript does. Java `extends`/`implements` references resolve like types.

- [ ] Tests: `service.py`'s `Store` import resolves to `pylib.store`; `Service.lookup` → `fallback` is `local`; `MemStore` implements/extends `Store` edges exist for Python (extends) and Java (implements); Java `Service.lookup` → `store.get(key)` is ambiguous between `Store.get` and `MemStore.get`; `MemStoreTest.putThenGet` appears in `tests MemStore.put` for both overloads of `put` (the call `put("a","b")` has arity 2 and both overloads have arity 2, so it is ambiguous between them, and `tests` lists the test under both).
- [ ] Implement; pass; lint; commit `feat(resolve): python and java identities and import resolution`.

### Task 7: Config extractors

`internal/extract/cfg/types.go`:

```go
// KeyDef is one place where a config key gets a value.
type KeyDef struct {
	Key     string // normalized key (see below)
	Line    int
	Preview string // value text, single line, at most 80 bytes
}

// KeyRef is one place where a config file refers to another key, e.g. ${X}.
type KeyRef struct {
	Key  string
	Line int
}

// Result is everything extracted from one config file.
type Result struct {
	Format string // yaml, json, xml, properties, env, hcl, shell, template
	Defs   []KeyDef
	Refs   []KeyRef
}

// Extractor parses one config format. Implementations are pure.
type Extractor interface {
	Extract(path string, src []byte) (*Result, error)
}
```

Format detection (`registry.go`): `.yaml`/`.yml` → yaml; `.json` (not `package.json`, `tsconfig*.json`, `package-lock.json`) → json; `.xml` (including `pom.xml`) → xml; `.properties` → properties; `.env`, `.env.*`, `*.env` → env; `.tf`, `.hcl`, `.tfvars` → hcl; `.sh`, `.bash`, files with a `sh`/`bash` shebang, and extension-less files under `bin/` or `scripts/` with a shebang → shell; `.jsp`, `.tpl`, `.tmpl`, `.j2`, `.template` → template.

Per-format rules:

| Format | Definitions | References |
|---|---|---|
| yaml | every scalar leaf as a dotted path of mapping keys; sequence items as `path.N`; mapping keys that are themselves leaves count once | `${X}` and `${X:default}` inside scalar values |
| json | as yaml | as yaml |
| xml | elements with text content as `a.b.c` (element path from the root, excluding the root element name); attributes as `a.b@attr`; Maven `<properties>` children additionally as their bare name (`lib.mode`) | `${X}` in text and attribute values |
| properties | `key=value` and `key: value`, continuation lines joined | `${X}` in values |
| env | `KEY=value`, optional `export ` prefix | `${X}` and `$X` in values |
| hcl | `variable "x"` blocks as `var.x`; `locals` attributes as `local.x`; top-level attributes as their name; resource attributes as `type.name.attr` | `var.x`, `local.x` traversals in expressions |
| shell | assignments `X=...` and `export X=...` (tree-sitter-bash `variable_assignment`) | `$X` and `${X}` expansions (`simple_expansion`, `expansion`) |
| template | none | `${X}` and `${X:default}` |

Keys are case-preserving. Previews are trimmed and cut to 80 bytes. Secrets: when the key contains `password`, `secret`, `token`, or `key` (case-insensitive, as a whole segment), the preview is `***`.

**Fixture** `testdata/ws/cfg-repo/` with one small file per format that defines `LIB_MODE`, `lib.mode`, `API_URL`, `PYLIB_MODE`, `JAVALIB_HOME` across formats, a templated `${API_URL}` in `web/index.jsp`, a Terraform `variable "region"` used by `local.zone`, and a `db.password` key to exercise masking.

- [ ] One test file per format with table tests on inline inputs (lines, keys, previews, refs, masking) plus a golden over the fixture.
- [ ] Implement one file per format; commit `feat(extract/cfg): config key extractors for eight formats`.

### Task 8: Storing config keys, matching code reads

- Store: `ReplaceFileConfig(fileID, repoID, format string, defs, refs)` upserts `config_key(repo_id, key)` rows and replaces the file's `config_use` rows (`defines` for defs with format and preview, `references` for refs). `ConfigKeysByName(name)` across repos, `ConfigUses(keyID)`, `ConfigKeysMatching(glob)`.
- Pipeline: discovered files with a config format (and no code extractor) go through `cfg`; record `file.lang` as the format.
- Resolver (`internal/resolve/config.go`): every `RefConfig` candidate is matched against `config_key` by exact name in every repo of the workspace; each match is a `config_use` row of kind `reads` with the enclosing symbol; unmatched candidates go to `reference_unresolved` with kind `config` (spec 7.2). When a run adds config keys, files holding unresolved `config` references with those names are re-resolved (reuse the Plan 1 name-index re-resolution).
- Template and config `references` rows also link to the key row by name, workspace-wide.

- [ ] Tests over the indexed workspace (all fixtures in one config): `LIB_MODE` has `defines` rows in cfg-repo and a `reads` row from `go-lib` `Mode`; adding a new key to a copy of `cfg-repo/.env` and re-indexing turns a previously unresolved candidate into a `reads` row; a key defined in two formats has one key row per repo and two `defines` rows.
- [ ] Commit `feat(resolve): config keys stored and matched against code reads workspace-wide`.

### Task 9: The `config` query

`codedoc config <key-or-glob> [--defines] [--reads] [--repo R]` (spec section 9). Output sections: `defines` (repo:path:line, format, preview), `references` (config files and templates referring to the key), `reads` (stable key of the reading symbol, path:line, signature, one-line doc). Without flags all three sections render. A glob (`lib.*`, `*_MODE`) lists matching keys first with counts, then the sections for each key until the budget runs out.

- [ ] Query tests with snapshot files and an e2e CLI test; commit `feat(query): config`.

### Task 10: README and status

Update `README.md` (languages, config formats, the `config` command) and `codedoc status` (per repo: config keys count, unresolved config candidates count). Commit `docs: python, java and config support`.

## Implementation notes

This plan is historical; the spec describes the code as built. Main deviations:

- Shell detection is by `.sh`/`.bash` or an sh/bash shebang on extension-less files, wherever they are (spec 7.1, 7.2).
- Secret masking matches more key words (passwd, credentials, api/private/access/signing/encryption keys) (spec 7.2).
- Java record components yield a private field and a public accessor, treated as synthetic by `gaps`; constructors are `<init>#hash` (spec 7.2, 5.1).
- Accessors reading a key through a constant (`os.Getenv(KeyConst)`) resolve via `symbol.value` and `reference.config_key` (migration 9; spec 7.2).
- `config` tags reads of keys no file defines `[undefined]` (spec 9).
- Duplicate module identities warn at `codedoc index`, not at config load (spec 7.3).
- The Gradle identity comes from `settings.gradle[.kts]`; Cargo and `.csproj` identities are left for those languages (spec 7.3).
