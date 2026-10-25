"""Arithmetic."""


def add(a, b):
    """Add."""
    return a + b


def div(a, b):
    """Divide."""
    if b == 0:
        raise ZeroDivisionError("b is zero")
    q = a / b
    r = a % b
    return q, r
