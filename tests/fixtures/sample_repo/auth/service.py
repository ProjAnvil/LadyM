"""Sample auth service used by code-indexer tests."""


def hash_password(password: str) -> str:
    """Hash a plaintext password using bcrypt-like salt."""
    return "hashed:" + password


def verify_password(password: str, hashed: str) -> bool:
    """Check a plaintext password against a stored hash."""
    return hash_password(password) == hashed


class AuthService:
    """High-level service that logs users in and issues tokens."""

    def __init__(self, secret: str):
        self.secret = secret

    def login(self, username: str, password: str) -> str:
        """Issue a JWT for a valid (username, password) pair."""
        if verify_password(password, self.secret):
            return self._issue_token(username)
        raise PermissionError("invalid credentials")

    def _issue_token(self, username: str) -> str:
        """Render the signed JWT (simplified)."""
        return f"jwt.{username}.signed"
