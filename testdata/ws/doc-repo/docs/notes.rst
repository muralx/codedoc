=============
Service notes
=============

Notes on the service package.

Lookup
======

Lookup reads one record.

Caching
-------

Lookups go through the cache first.

Eviction
~~~~~~~~

The oldest entry goes first.

Writes
======

Writes bypass the cache::

    ====
    not a title
    ====
