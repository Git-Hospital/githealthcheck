"""Reports."""


def summary(values, weight, limit):
    """Describe the values."""
    total, count = 0, 0
    for v in values:
        if v < 0:
            continue
        total += v * weight
        count += 1
    if count == 0:
        return "none"
    mean = total // count
    label = "%d items, mean %d" % (count, mean)
    if mean > limit:
        label += " (over the limit)"
    spread = 0
    for v in values:
        diff = v * weight - mean
        if diff > spread:
            spread = diff
        elif -diff > spread:
            spread = -diff
    label += ", spread %d" % spread
    return label


def report(xs, scale, top, title):
    """Describe the values, with a title."""
    return title + ": " + summary(xs, scale, top)


def run(values):
    """Run."""
    return summary(values, 1, 2) + report(values, 1, 2, "t")
