from flask import (
    Blueprint,
    request,
    jsonify,
    url_for,
    render_template,
    redirect,
)
from flask_jwt_extended import (
    create_access_token,
    create_refresh_token,
    set_refresh_cookies,
    jwt_required,
    get_jwt_identity,
    unset_jwt_cookies,
)

import uuid

from webserver.db import user as User
from webserver.db import storage as Storage

metadata_bp = Blueprint("metadata", __name__)


@metadata_bp.post("/insert-metadata")
@jwt_required()
def insert_metadata():
    data = request.json
    email_id = get_jwt_identity()
    file_path = data["file_path"]
    size = data["size"]
    try:
        Storage.insert_s3_metadata(email_id, file_path, size)
    except Exception as err:
        return jsonify({"error": err}), 500

    return jsonify({"status": "okay"})


@metadata_bp.post("/get-metadata")
@jwt_required()
def get_metadata():
    email_id = get_jwt_identity()
    metadata = Storage.get_metadata_for_user(email_id)

    total_file_size = 0
    for row in metadata:
        total_file_size += row["file_size"]

    return jsonify({"total_file_size": total_file_size}), 200
