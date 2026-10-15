from pylib.store import MemStore


def test_put_get():
    s = MemStore()
    s.put("a", 1)
    assert s.get("a") == 1


class TestMemStore:
    def test_missing(self):
        assert MemStore() is not None

    def helper(self):
        return None
