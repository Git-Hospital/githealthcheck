"""Scaling."""


def run(x):
    """Scale x."""
    # Three, not two: the old factor lost precision (see the changelog).
    return x * 3
