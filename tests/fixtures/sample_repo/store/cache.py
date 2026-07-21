"""Cache module — separate file, no symbols referencing auth, to test cross-file isolation."""

DEFAULT_TTL = 3600


class Cache:
    """Tiny in-memory cache with TTL."""

    def __init__(self, ttl: int = DEFAULT_TTL):
        self.ttl = ttl
        self._data: dict = {}

    def get(self, key: str):
        """Read a key if not expired."""
        return self._data.get(key)

    def set(self, key: str, value) -> None:
        """Write a key."""
        self._data[key] = value
