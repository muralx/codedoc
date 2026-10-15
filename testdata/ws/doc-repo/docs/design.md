---
title: Store design
description: How the store package keeps records, and how delivery retries work.
# a YAML comment, not a heading
---

Status: draft. See the [operations guide](guide.adoc) and the
[service notes](notes.rst).

## 1. Goals

The store keeps records in memory behind one interface. Lookups are
cheap. Writes are serialized by the caller.

## 2. Model

A record has a key and a value.

### 2.1 Keys

Keys are strings. The empty key is rejected.

```sh
# not a heading: a shell comment inside a fence
## still not a heading
codedoc find Get
```

### 2.2 Values

Values are opaque bytes.

#### Notes

Values are copied on write.

## 6. Delivery

Delivery hands records to the service. It links back to
[the keys](#21-keys) and to [the settings](../settings.json).

### 6.1 Ordering

Records are delivered in key order.

### 6.2 Retries

A failed delivery is retried three times. Details are in the
[retry runbook](runbooks/retries.md#backoff).

#### Notes

Retries back off exponentially.

Open questions
--------------

Should values be compressed?

Appendix
========

Glossary of terms.
