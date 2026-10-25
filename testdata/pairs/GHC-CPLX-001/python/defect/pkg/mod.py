"""Totals."""


def run(items, limit):
    """Sum the parts of the items under a limit."""
    total = 0
    for item in items:
        if item is None:
            continue
        for part in item:
            if part > limit:
                if part % 2:
                    total += part
                else:
                    total -= part
            elif part < 0 and limit:
                total += 1
    while total > limit:
        total //= 2
    return total
