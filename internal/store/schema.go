package store

// migrations are applied in order; the meta table records the version.
var migrations = []string{
	`
CREATE TABLE workspace(id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL);
CREATE TABLE repo(id INTEGER PRIMARY KEY, workspace_id INTEGER NOT NULL REFERENCES workspace(id),
  name TEXT NOT NULL, path TEXT NOT NULL UNIQUE, vcs TEXT NOT NULL DEFAULT 'git',
  branch TEXT, "commit" TEXT, indexed_at INTEGER, scip_commit TEXT, UNIQUE(workspace_id, name));
CREATE TABLE module(id INTEGER PRIMARY KEY, repo_id INTEGER NOT NULL REFERENCES repo(id) ON DELETE CASCADE,
  path TEXT NOT NULL, lang TEXT NOT NULL, kind TEXT NOT NULL, identity TEXT NOT NULL,
  parent_id INTEGER REFERENCES module(id), doc_text TEXT, doc_source TEXT, doc_state TEXT NOT NULL DEFAULT 'missing',
  UNIQUE(repo_id, lang, path));
CREATE INDEX module_identity ON module(identity);
CREATE TABLE file(id INTEGER PRIMARY KEY, repo_id INTEGER NOT NULL REFERENCES repo(id) ON DELETE CASCADE,
  path TEXT NOT NULL, lang TEXT, size INTEGER NOT NULL, mtime INTEGER NOT NULL, hash TEXT NOT NULL,
  lines INTEGER NOT NULL DEFAULT 0, generated INTEGER NOT NULL DEFAULT 0, parse_errors INTEGER NOT NULL DEFAULT 0,
  last_commit TEXT, last_commit_at INTEGER, UNIQUE(repo_id, path));
CREATE TABLE symbol(id INTEGER PRIMARY KEY, file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE,
  module_id INTEGER REFERENCES module(id), kind TEXT NOT NULL, name TEXT NOT NULL, qualified_name TEXT NOT NULL,
  stable_key TEXT NOT NULL, container_id INTEGER REFERENCES symbol(id),
  start_line INTEGER NOT NULL, end_line INTEGER NOT NULL, start_byte INTEGER NOT NULL, end_byte INTEGER NOT NULL,
  sig_start_line INTEGER NOT NULL, sig_end_line INTEGER NOT NULL, signature TEXT NOT NULL,
  visibility TEXT NOT NULL, is_test INTEGER NOT NULL DEFAULT 0, body_hash TEXT NOT NULL,
  doc_text TEXT, doc_hash TEXT, doc_body_hash TEXT, doc_state TEXT NOT NULL DEFAULT 'missing',
  doc_start_line INTEGER, doc_end_line INTEGER);
CREATE INDEX symbol_name ON symbol(name);
CREATE UNIQUE INDEX symbol_key ON symbol(stable_key);
CREATE INDEX symbol_file ON symbol(file_id);
CREATE TABLE edge(id INTEGER PRIMARY KEY, src_symbol_id INTEGER NOT NULL REFERENCES symbol(id) ON DELETE CASCADE,
  dst_symbol_id INTEGER REFERENCES symbol(id) ON DELETE CASCADE, kind TEXT NOT NULL, ref_kind TEXT NOT NULL, confidence TEXT NOT NULL,
  file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE, line INTEGER NOT NULL);
CREATE INDEX edge_dst ON edge(dst_symbol_id, kind);
CREATE INDEX edge_src ON edge(src_symbol_id, kind);
CREATE TABLE edge_candidate(edge_id INTEGER NOT NULL REFERENCES edge(id) ON DELETE CASCADE,
  symbol_id INTEGER NOT NULL REFERENCES symbol(id) ON DELETE CASCADE);
CREATE INDEX edge_candidate_symbol ON edge_candidate(symbol_id);
CREATE TABLE module_edge(src_module_id INTEGER NOT NULL REFERENCES module(id) ON DELETE CASCADE,
  dst_module_id INTEGER NOT NULL REFERENCES module(id) ON DELETE CASCADE, kind TEXT NOT NULL, confidence TEXT NOT NULL,
  PRIMARY KEY(src_module_id, dst_module_id, kind));
CREATE TABLE import(file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE, raw TEXT NOT NULL, alias TEXT,
  resolved_module_id INTEGER REFERENCES module(id), line INTEGER NOT NULL);
CREATE INDEX import_file ON import(file_id);
CREATE TABLE config_key(id INTEGER PRIMARY KEY, repo_id INTEGER NOT NULL REFERENCES repo(id) ON DELETE CASCADE,
  key TEXT NOT NULL, UNIQUE(repo_id, key));
CREATE INDEX config_key_key ON config_key(key);
CREATE TABLE config_use(key_id INTEGER NOT NULL REFERENCES config_key(id) ON DELETE CASCADE,
  file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE, symbol_id INTEGER REFERENCES symbol(id) ON DELETE CASCADE,
  line INTEGER NOT NULL, kind TEXT NOT NULL, format TEXT NOT NULL, value_preview TEXT);
CREATE INDEX config_use_file ON config_use(file_id);
CREATE TABLE repo_identity(repo_id INTEGER NOT NULL REFERENCES repo(id) ON DELETE CASCADE, lang TEXT NOT NULL,
  identity TEXT NOT NULL, path TEXT NOT NULL, PRIMARY KEY(repo_id, lang, identity));
CREATE TABLE reference_unresolved(file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE,
  symbol_id INTEGER REFERENCES symbol(id) ON DELETE CASCADE, name TEXT NOT NULL, line INTEGER NOT NULL, kind TEXT NOT NULL);
CREATE INDEX reference_unresolved_name ON reference_unresolved(name);
CREATE TABLE scip_symbol(stable_key TEXT PRIMARY KEY, scip_symbol TEXT NOT NULL);
CREATE VIRTUAL TABLE symbol_fts USING fts5(name, qualified_name, signature, doc_text, content='symbol', content_rowid='id');
CREATE VIRTUAL TABLE module_fts USING fts5(path, identity, doc_text, content='module', content_rowid='id');
CREATE TRIGGER symbol_ai AFTER INSERT ON symbol BEGIN
  INSERT INTO symbol_fts(rowid, name, qualified_name, signature, doc_text) VALUES (new.id, new.name, new.qualified_name, new.signature, new.doc_text); END;
CREATE TRIGGER symbol_ad AFTER DELETE ON symbol BEGIN
  INSERT INTO symbol_fts(symbol_fts, rowid, name, qualified_name, signature, doc_text) VALUES ('delete', old.id, old.name, old.qualified_name, old.signature, old.doc_text); END;
CREATE TRIGGER symbol_au AFTER UPDATE ON symbol BEGIN
  INSERT INTO symbol_fts(symbol_fts, rowid, name, qualified_name, signature, doc_text) VALUES ('delete', old.id, old.name, old.qualified_name, old.signature, old.doc_text);
  INSERT INTO symbol_fts(rowid, name, qualified_name, signature, doc_text) VALUES (new.id, new.name, new.qualified_name, new.signature, new.doc_text); END;
CREATE TRIGGER module_ai AFTER INSERT ON module BEGIN
  INSERT INTO module_fts(rowid, path, identity, doc_text) VALUES (new.id, new.path, new.identity, new.doc_text); END;
CREATE TRIGGER module_ad AFTER DELETE ON module BEGIN
  INSERT INTO module_fts(module_fts, rowid, path, identity, doc_text) VALUES ('delete', old.id, old.path, old.identity, old.doc_text); END;
CREATE TRIGGER module_au AFTER UPDATE ON module BEGIN
  INSERT INTO module_fts(module_fts, rowid, path, identity, doc_text) VALUES ('delete', old.id, old.path, old.identity, old.doc_text);
  INSERT INTO module_fts(rowid, path, identity, doc_text) VALUES (new.id, new.path, new.identity, new.doc_text); END;
`,
	// 2: named imports (TS/JS) for symbol resolution through imports, as a JSON array.
	`ALTER TABLE import ADD COLUMN names TEXT;`,
	// 3: find the imports resolved to a module (module deletion, re-resolution).
	`CREATE INDEX import_resolved ON import(resolved_module_id);`,
	// 4: every reference is stored so it can be resolved again when the
	// symbols it may name change; edges point back at the reference they
	// came from, and reference_unresolved becomes a view of the references
	// without an edge (spec section 8).
	`
CREATE TABLE reference(id INTEGER PRIMARY KEY, file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE,
  symbol_id INTEGER REFERENCES symbol(id) ON DELETE CASCADE, kind TEXT NOT NULL, name TEXT NOT NULL,
  qualifier TEXT NOT NULL, line INTEGER NOT NULL, arity INTEGER NOT NULL, candidates INTEGER NOT NULL DEFAULT 0);
CREATE INDEX reference_file ON reference(file_id);
CREATE INDEX reference_symbol ON reference(symbol_id);
CREATE INDEX reference_name ON reference(name);
ALTER TABLE edge ADD COLUMN reference_id INTEGER REFERENCES reference(id) ON DELETE CASCADE;
CREATE INDEX edge_reference ON edge(reference_id);
CREATE INDEX edge_file ON edge(file_id);
CREATE INDEX edge_candidate_edge ON edge_candidate(edge_id);
CREATE INDEX symbol_module ON symbol(module_id, kind);
CREATE INDEX symbol_kind ON symbol(kind);
DROP TABLE reference_unresolved;
CREATE VIEW reference_unresolved AS SELECT r.file_id, r.symbol_id, r.name, r.line, r.kind, r.candidates
  FROM reference r WHERE NOT EXISTS (SELECT 1 FROM edge e WHERE e.reference_id = r.id);
`,
	// 5: a code read of a config key points back at the reference it was
	// matched from, so a matched config candidate leaves the unresolved
	// view and is matched again when the reference is (spec 7.2).
	`
ALTER TABLE config_use ADD COLUMN reference_id INTEGER REFERENCES reference(id) ON DELETE CASCADE;
CREATE INDEX config_use_reference ON config_use(reference_id);
CREATE INDEX config_use_key ON config_use(key_id, kind);
DROP VIEW reference_unresolved;
CREATE VIEW reference_unresolved AS SELECT r.file_id, r.symbol_id, r.name, r.line, r.kind, r.candidates
  FROM reference r WHERE NOT EXISTS (SELECT 1 FROM edge e WHERE e.reference_id = r.id)
  AND NOT EXISTS (SELECT 1 FROM config_use u WHERE u.reference_id = r.id);
`,
	// 6: body hashes now cover the signature text (spec 7.5) and docs
	// carry a staleness state: every file is extracted again, and kept
	// symbols lose their body hash, so their doc state is seeded again
	// from git blame like a new symbol's.
	`
UPDATE symbol SET body_hash = '';
UPDATE file SET hash = '', mtime = 0;
`,
	// 7: embedding vectors of documented symbols and modules, as
	// little-endian float32 BLOBs scored by a brute-force cosine scan
	// (spec 7.7); text_hash identifies the text a vector was computed from.
	`
CREATE TABLE symbol_vec(symbol_id INTEGER PRIMARY KEY REFERENCES symbol(id) ON DELETE CASCADE,
  text_hash TEXT NOT NULL, vec BLOB NOT NULL);
CREATE TABLE module_vec(module_id INTEGER PRIMARY KEY REFERENCES module(id) ON DELETE CASCADE,
  text_hash TEXT NOT NULL, vec BLOB NOT NULL);
`,
	// 8: the 0-based byte column of a reference's name, so a SCIP
	// occurrence matches its reference row by position even when one line
	// uses a name twice; -1 on rows extracted before (the extractor
	// version bump re-extracts them). added marks the rows a SCIP merge
	// added for occurrences the extractor did not record, which a later
	// merge deletes when its index no longer confirms them.
	`
ALTER TABLE reference ADD COLUMN col INTEGER NOT NULL DEFAULT -1;
ALTER TABLE reference ADD COLUMN added INTEGER NOT NULL DEFAULT 0;
`,
	// 9: the string value of a constant, and the config key a config
	// accessor reads through a constant name (os.Getenv(KeyConst)), set
	// by the resolver from the value of the constant the name resolves to
	// (spec 7.2); the index finds those reads again when that key is
	// defined or removed.
	`
ALTER TABLE symbol ADD COLUMN value TEXT;
ALTER TABLE reference ADD COLUMN config_key TEXT;
CREATE INDEX reference_config_key ON reference(config_key) WHERE config_key IS NOT NULL;
`,
	// 10: the extractor version each file's rows were extracted with, so
	// a catch-up after a version bump that stops part way keeps the files
	// it re-extracted (spec 7.8). Rows take the version recorded for their
	// repo by path, which this replaces. Indexes on the columns that
	// reference a symbol without one let a symbol delete look its children
	// up instead of scanning those tables (foreign key checks and
	// cascades).
	`
ALTER TABLE file ADD COLUMN extracted_version TEXT NOT NULL DEFAULT '';
CREATE INDEX symbol_container ON symbol(container_id);
CREATE INDEX config_use_symbol ON config_use(symbol_id);
UPDATE file SET extracted_version = COALESCE((SELECT m.value FROM meta m JOIN repo r
  ON m.key = 'extractor_version:' || r.path WHERE r.id = file.repo_id), '');
DELETE FROM meta WHERE key LIKE 'extractor_version:%';
`,
	// 11: the third-party packages the manifests of a repo resolve to
	// (deps spec section 4), one row per manifest and package, and the
	// manifests, lock files and install markers discovery read them from,
	// with the size, mtime and content hash that tell when to run it
	// again. The packages themselves live in the shared deps.db.
	`
CREATE TABLE dep_use(repo_id INTEGER NOT NULL REFERENCES repo(id) ON DELETE CASCADE,
  manifest_path TEXT NOT NULL, eco TEXT NOT NULL, name TEXT NOT NULL, version TEXT NOT NULL,
  declared TEXT NOT NULL, state TEXT NOT NULL, dir TEXT NOT NULL DEFAULT '', types_dir TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(repo_id, manifest_path, eco, name));
CREATE INDEX dep_use_package ON dep_use(eco, name, version);
CREATE TABLE dep_manifest(repo_id INTEGER NOT NULL REFERENCES repo(id) ON DELETE CASCADE,
  path TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL, hash TEXT NOT NULL,
  PRIMARY KEY(repo_id, path));
`,
	// 12: an edge from workspace code into a third-party package (deps
	// spec section 6): dst_symbol_id is NULL and dst_dep_key holds the
	// stable key of the dependency symbol, which lives in deps.db.
	`
ALTER TABLE edge ADD COLUMN dst_dep_key TEXT;
CREATE INDEX edge_dst_dep ON edge(dst_dep_key, kind) WHERE dst_dep_key IS NOT NULL;
`,
	// 13: documents (documents spec sections 2 and 3). A file row carries
	// the title codedoc ls prints. section_fts holds the own text of each
	// document and section symbol, keyed by symbol id, which the symbol
	// text index (heading and summary only) does not cover; a deleted
	// symbol takes its text along. doc_link holds the relative links of a
	// document by the section they are written in; the references edges
	// between documents are derived from it.
	`
ALTER TABLE file ADD COLUMN title TEXT NOT NULL DEFAULT '';
CREATE VIRTUAL TABLE section_fts USING fts5(text);
CREATE TRIGGER section_fts_ad AFTER DELETE ON symbol WHEN old.kind IN ('section', 'document') BEGIN
  DELETE FROM section_fts WHERE rowid = old.id; END;
CREATE TABLE doc_link(file_id INTEGER NOT NULL REFERENCES file(id) ON DELETE CASCADE,
  symbol_id INTEGER NOT NULL REFERENCES symbol(id) ON DELETE CASCADE,
  target TEXT NOT NULL, anchor TEXT NOT NULL, line INTEGER NOT NULL);
CREATE INDEX doc_link_file ON doc_link(file_id);
CREATE INDEX doc_link_symbol ON doc_link(symbol_id);
`,
	// 14: the type of the variable a reference's qualifier starts with,
	// where the extractor found it evident (extract.Reference.RecvType).
	`ALTER TABLE reference ADD COLUMN recv_type TEXT NOT NULL DEFAULT '';`,
	// 15: the name a module's sources declare for it when that is not
	// the last segment of its identity: the Go package yaml of
	// gopkg.in/yaml.v3 (deps spec section 3). Empty otherwise.
	`ALTER TABLE module ADD COLUMN name TEXT NOT NULL DEFAULT '';`,
	// 16: the name the installed package has in its own manifest, which
	// identifies it in deps.db: lodash-es for the npm alias "lodash":
	// "npm:lodash-es@4" (deps spec section 4). name stays what the
	// manifest declares, the head of the import specifiers.
	`
ALTER TABLE dep_use ADD COLUMN package TEXT NOT NULL DEFAULT '';
UPDATE dep_use SET package = name;
DROP INDEX dep_use_package;
CREATE INDEX dep_use_package ON dep_use(eco, package, version);
`,
	// 17: in deps.db, which workspace databases use each package version
	// and when a sync last said so (deps spec section 9): what codedoc
	// deps prune keeps for the workspaces the config in use does not
	// list. Empty in a workspace database.
	`
CREATE TABLE dep_user(package TEXT NOT NULL, workspace_db TEXT NOT NULL, seen_at INTEGER NOT NULL,
  PRIMARY KEY(package, workspace_db));
`,
	// 18: indexes on the referencing columns that had none, so deleting a
	// module looks up the module edges into it and its child modules
	// instead of scanning their tables (as do dependents and the fan-in
	// of gaps), and replacing the package versions a workspace database
	// uses finds its dep_user rows.
	`
CREATE INDEX module_edge_dst ON module_edge(dst_module_id);
CREATE INDEX module_parent ON module(parent_id);
CREATE INDEX dep_user_workspace ON dep_user(workspace_db);
`,
	// 19: documentation upkeep (efficiency spec section 7): the last
	// documented commit of each branch of a repo, with when a run last
	// finished there, and what the workspace's runs spent per day.
	`
CREATE TABLE upkeep_branch(repo_id INTEGER NOT NULL REFERENCES repo(id) ON DELETE CASCADE, branch TEXT NOT NULL,
  "commit" TEXT NOT NULL, ran_at INTEGER NOT NULL, PRIMARY KEY(repo_id, branch));
CREATE TABLE upkeep_spend(day TEXT PRIMARY KEY, usd REAL NOT NULL);
`,
	// 20: several vectors per symbol, one per passage of its embedded text
	// (efficiency spec section 3): ord numbers them, start_line and
	// end_line are the lines a passage covers, counted from the first line
	// of the symbol's doc (from_doc 1) or of the symbol, so they stay right
	// when the symbol moves in its file. A vector of the scheme before
	// becomes passage 0: still current (text hash prefixed p1:) where the
	// text embedded is the same, a documented symbol or a section of at
	// most 1,200 bytes; otherwise it keeps its old hash, so it serves
	// search until the next embedding pass replaces it.
	`
ALTER TABLE symbol_vec RENAME TO symbol_vec_v1;
CREATE TABLE symbol_vec(symbol_id INTEGER NOT NULL REFERENCES symbol(id) ON DELETE CASCADE,
  ord INTEGER NOT NULL, from_doc INTEGER NOT NULL, start_line INTEGER NOT NULL, end_line INTEGER NOT NULL,
  text_hash TEXT NOT NULL, vec BLOB NOT NULL, PRIMARY KEY(symbol_id, ord));
INSERT INTO symbol_vec(symbol_id, ord, from_doc, start_line, end_line, text_hash, vec)
  SELECT v.symbol_id, 0, 0, 0, s.end_line - s.start_line, v.text_hash, v.vec
  FROM symbol_vec_v1 v JOIN symbol s ON s.id = v.symbol_id;
DROP TABLE symbol_vec_v1;
UPDATE symbol_vec SET text_hash = 'p1:' || text_hash WHERE symbol_id IN (SELECT s.id FROM symbol s
  WHERE s.kind NOT IN ('document', 'section') AND length(CAST(COALESCE(s.doc_text, '') AS BLOB)) BETWEEN 1 AND 1200);
UPDATE symbol_vec SET from_doc = 1,
  end_line = (SELECT s.doc_end_line - s.doc_start_line FROM symbol s WHERE s.id = symbol_vec.symbol_id)
  WHERE text_hash LIKE 'p1:%' AND symbol_id IN (SELECT s.id FROM symbol s WHERE s.doc_start_line IS NOT NULL);
UPDATE symbol_vec SET text_hash = 'p1:' || text_hash,
  end_line = (SELECT MAX(0, COALESCE((SELECT MIN(m.start_line) - 1 FROM symbol m WHERE m.file_id = s.file_id
    AND m.id <> s.id AND m.start_line >= s.start_line + (s.kind = 'section') AND m.start_line <= s.end_line), s.end_line)
    - s.start_line) FROM symbol s WHERE s.id = symbol_vec.symbol_id)
  WHERE symbol_id IN (SELECT s.id FROM symbol s WHERE s.kind IN ('document', 'section')
    AND COALESCE((SELECT length(CAST(t.text AS BLOB)) FROM section_fts t WHERE t.rowid = s.id), 0) <= 1200);
`,
	// 21: whether a manifest declares a package for development only
	// (npm devDependencies): its vectors are embedded after those of the
	// packages the code runs with (deps spec section 8). Rows written
	// before read as runtime until their manifest is discovered again.
	`ALTER TABLE dep_use ADD COLUMN dev INTEGER NOT NULL DEFAULT 0;`,
}
