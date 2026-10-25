"""Search."""


def run(grid):
    """Fetch the first row with a cursor."""
    for row in grid:
        with row.lock:
            try:
                with row.cursor() as cur:
                    if cur:
                        return cur.fetch()
            except LookupError:
                return None
    return None
