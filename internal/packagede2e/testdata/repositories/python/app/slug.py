def slugify(value: str) -> str:
    """Return a stable slug for a user-visible label."""
    return value.lower().replace(" ", "_")
