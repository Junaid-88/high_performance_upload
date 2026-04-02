from functools import wraps
from flask import request, current_app, jsonify
import jwt
from webserver.db import user as User


def token_required(f):
    @wraps(f)
    def decorated(*args, **kwargs):
        token = request.cookies.get("jwt_token")

        if not token:
            return jsonify({"message": "Token is missing!"}), 401

        try:
            data = jwt.decode(token, current_app.config["SECRET_KEY"], algorithms=["HS256"])
            current_user = User.get_user_by_public_id(data["public_id"])
        except Exception as err:
            return jsonify({"message": f"Token is invalid! {err}"}), 401

        return f(current_user, *args, **kwargs)

    return decorated
