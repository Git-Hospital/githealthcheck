"""Totals."""


def part_value(part, limit):
    """The value one part adds."""
    if part > limit:
        return part if part % 2 else -part
    if part < 0 and limit:
        return 1
    return 0


def run(items, limit):
    """Sum the parts of the items under a limit."""
    total = 0
    for item in items:
        if item is None:
            continue
        total += sum(part_value(p, limit) for p in item)
    while total > limit:
        total //= 2
    return total
