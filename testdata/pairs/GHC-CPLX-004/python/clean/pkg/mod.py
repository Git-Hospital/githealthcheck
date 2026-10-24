"""Saving."""

import logging


def run(store, value):
    """Save a value."""
    try:
        store.save(value)
    except KeyError:
        pass
    except Exception:
        logging.exception("save failed")
        raise
