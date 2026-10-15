from pylib.store import Store
from . import store as backend


class Service:
    """Service looks keys up in a store."""

    def __init__(self, store: Store):
        self.store = store

    def lookup(self, key: str) -> str:
        found = self.store.get(key)
        if found is None:
            return fallback(key)
        return found


def fallback(key):
    """Return the default value for key."""
    return backend.DEFAULT_TIMEOUT
