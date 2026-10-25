"""Saving."""


def run(store, value):
    """Save a value."""
    try:
        store.save(value)
    except Exception:
        pass
