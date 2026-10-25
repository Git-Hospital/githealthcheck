"""Search."""


def fetch(row):
    """Fetch a row with its cursor."""
    with row.cursor() as cur:
        if cur:
            return cur.fetch()
    return None


def run(grid):
    """Fetch the first row with a cursor."""
    for row in grid:
        with row.lock:
            try:
                return fetch(row)
            except LookupError:
                return None
    return None
