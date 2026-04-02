from sqlalchemy import text

from webserver import db


def check_if_user_name_is_unique(user_name: str) -> bool:
    query = 'SELECT user_name FROM users WHERE user_name = :user_name'
    with db.engine.connect() as conn:
        res = conn.execute(text(query), {"user_name": user_name})
        if res.rowcount == 0:
            return True
        else:
            return False


def register_user(user_name: str, email_id: str, password_hash: str, public_id: str):
    query = "INSERT INTO users (user_name, email_id, password_hash, public_id) VALUES (:user_name, :email_id, :password_hash, :public_id)"
    with db.engine.begin() as conn:
        conn.execute(
            text(query),
            {"user_name": user_name, "email_id": email_id, "password_hash": password_hash, "public_id": public_id},
        )


def get_user(email_id: str):
    query = "SELECT * FROM users WHERE email_id=:email_id"
    with db.engine.connect() as conn:
        res = conn.execute(text(query), {"email_id": email_id})
        return res.mappings().fetchone()


def get_user_by_public_id(public_id: str):
    query = "SELECT * FROM users WHERE public_id = :public_id"
    with db.engine.connect() as conn:
        res = conn.execute(text(query), {"public_id": public_id})
        return res.mappings().fetchone()