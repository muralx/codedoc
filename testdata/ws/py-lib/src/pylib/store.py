"""Key-value stores of the Python fixture.

Two implementations share one interface.
"""

import abc
import os

DEFAULT_TIMEOUT = 30
_registry = {}


class Store(abc.ABC):
    """Store is a key-value store."""

    @abc.abstractmethod
    def get(self, key):
        """Return the value stored under key."""

    @abc.abstractmethod
    def put(self, key, value):
        """Store value under key."""


class MemStore(Store):
    """MemStore keeps values in memory.

    Values live in a dict:
        indented lines keep their relative indent.
    """

    def __init__(self):
        self.data = {}
        self.__hits = 0
        self.data = dict()

    def get(self, key):
        """Return the value for key or raise KeyError."""
        if key not in self.data:
            raise _missing(key)
        return self.data[key]

    def put(self, key, value):
        self.data[key] = value


def _missing(key):
    return KeyError(key)


async def mode() -> str:
    return os.environ.get("PYLIB_MODE", "dev")
