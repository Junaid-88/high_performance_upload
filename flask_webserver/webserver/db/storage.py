from sqlalchemy import text

from webserver import db


def insert_s3_metadata(email_id: str, file_path: str, size: int):
    query = "INSERT INTO s3_metadata (email_id, file_path, file_size) VALUES (:email_id, :file_path, :file_size)"
    with db.engine.begin() as conn:
        conn.execute(text(query), {"email_id": email_id, "file_path": file_path, "file_size": size})


def get_metadata_for_user(email_id: str) -> list[dict]:
    query = "SELECT * FROM s3_metadata WHERE email_id = :email_id"
    with db.engine.connect() as conn:
        res = conn.execute(text(query), {"email_id": email_id})
        return res.mappings().fetchall()
