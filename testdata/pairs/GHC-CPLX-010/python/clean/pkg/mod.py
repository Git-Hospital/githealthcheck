"""Clamping."""


def clamp(value, low, high):
    """Clamp a value."""
    if value < low:
        return low
    if value > high:
        return high
    return value


def run(x):
    """Run."""
    return clamp(x, 0, 1)
