"""Exporting."""


class Exporter:
    """A base class: subclasses export."""

    def export(self, data):
        """Export the data."""
        raise NotImplementedError


def run(data):
    """Export the data."""
    return list(data)
