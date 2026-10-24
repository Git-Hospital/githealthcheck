"""Clamping."""


def clamp(value, low, high):
    """Clamp a value."""
    if value < low:
        return low
    if value > high:
        return high
    return value


def bound(x, a, b):
    """Clamp a value."""
    if x < a:
        return a
    if x > b:
        return b
    return x


def run(x):
    """Run."""
    return clamp(x, 0, 1) + bound(x, 0, 1)
