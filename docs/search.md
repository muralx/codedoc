# Search and embeddings

`search` and `where` fuse two rankings:

- text: the full-text index of symbol names, qualified names, signatures
  and docs, of the text of every document section, and of module paths,
  identities and docs. Hits are merged by how many words of the query
  they hold. A word used only inside a body is found through the names
  the body references;
- meaning: cosine similarity between the query and the embedded
  passages, with a floor. A symbol or section is ranked once, by its
  best passage.

Test code is weighed down and the sections of one document are kept
apart. Each hit is the entry line and, under it, the lines of its file
that match: the passages closest to the query, then the runs of lines of
its doc comment and body that hold the most query words, three in all,
each with two lines of context (`-C`, `-A`, `-B`). Dependency hits and
modules have no lines: their files are not read.

```
$ codedoc search "exponential backoff" --lang doc --limit 3
doc-repo@c713ac5
== search "exponential backoff" (3)
Notes  docs/design.md:51-53  #### Notes  Retries back off exponentially.
51	#### Notes
52	
53	Retries back off exponentially.
Backoff  docs/runbooks/retries.md:6-8  ## Backoff  Wait one second, then double the wait after every failure.
6.2 Retries  docs/design.md:46-53  ### 6.2 Retries  Details are in the [retry runbook](runbooks/retries.md#backoff).
47	
48	A failed delivery is retried three times. Details are in the
49	[retry runbook](runbooks/retries.md#backoff).
50	
text search only: embeddings disabled (CODEDOC_EMBEDDER=off)
```

A hit does not print lines an earlier hit of the answer printed: the
third one above stops before the `Notes` the first one showed.

Embeddings come from nomic-embed-text-v1.5 run locally through ONNX
Runtime. Without them, search is text-only, and its note says so. With
the model, a query in other words than the code's finds it, and a
single strong hit of `where` is shown whole:

```
$ codedoc where "wait longer after every failure" --limit 2
doc-repo@c713ac5
== where "wait longer after every failure" (6)
Backoff  docs/runbooks/retries.md:6-8
6	## Backoff
7	
8	Wait one second, then double the wait after every failure.
5 more: codedoc where 'wait longer after every failure' --limit 2 --offset 1
```

**What is embedded.** Always real lines of the file as indexed:

| Owner | Text |
|---|---|
| document, section | its own lines, heading to the line before its first sub-section |
| documented symbol | path, container, signature, doc and the first 10 body lines; a doc over 1,200 bytes is embedded by passage, without the body |
| undocumented symbol | its declaration's lines, by passage. Not embedded: symbols of generated files, test cases, and fields, constants and one-line declarations (they are found by name) |
| undocumented class, struct, interface | path, signature and the names of its members |
| module | identity, path and doc |

A text of at most 1,200 bytes is one passage. A longer one is cut into
overlapping passages of at most 800 bytes of whole lines, at a blank
line or at a statement of the outermost level where there is one, 40
passages at most. Each passage keeps its line range, which is how a hit
shows the lines that match and not the first lines of the symbol.

To enable embeddings:

1. Install ONNX Runtime 1.17.1 (`libonnxruntime.so.1.17.1` from the
   [release archive](https://github.com/microsoft/onnxruntime/releases/tag/v1.17.1)).
   codedoc loads `/usr/local/lib/libonnxruntime.so` unless
   `CODEDOC_ONNXRUNTIME_LIB` names another path. It loads the library
   only when a process first embeds, so codedoc runs without it.
2. Download the model (about 550 MB): `make download-model` writes
   `models/nomic-embed-text-v1.5.onnx` and `models/tokenizer.json`.
3. Point `embed.model_dir` (or `CODEDOC_MODEL_DIR`) at that directory.
4. Run `codedoc index`. It embeds every text that changed;
   `--verbose` reports the line `vectors  +92 symbols +7 modules` after
   the repos, and
   `status` shows `vectors:N/M` per repo (`vectors:18/18` when nothing
   is left to embed).

One model call uses half the logical CPUs, at most 12 threads: past 12
a thread adds heat, not speed. `CODEDOC_EMBED_THREADS` sets another
number.

Vectors are float32 BLOBs in the workspace database, scored by a linear
scan. If the model or the library is missing, codedoc says so once on
stderr and carries on text-only.

Queries keep vectors current only in small steps:

- A query's freshness check embeds what it re-extracts, but only when
  the model is already loaded and at most 64 owners are stale.
- `search` catches up at most 64 stale owners before answering.
  Otherwise its note names the backlog.

So after large changes, run `codedoc index` or keep the daemon running;
the daemon holds the model loaded.
