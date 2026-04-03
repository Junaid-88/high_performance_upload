import os
from sqlalchemy import create_engine, text

SQL_DIR = os.path.join(os.path.dirname(os.path.realpath(__file__)), "..", "..", "postgre-sql")

engine = None


def init_db_engine(connect_str):
    global engine
    engine = create_engine(connect_str)


def exec_sql_script(sql_file_path):
    with open(sql_file_path) as sql:
        commands = sql.read().split(";")
        with engine.connect().execution_options(isolation_level="AUTOCOMMIT") as conn:
            for command in commands:
                command = command.strip()
                if command:
                    conn.execute(text(command))

def new_database_needed() -> bool:
    with engine.connect() as conn:
        result = conn.execute(text("""select * from pg_database"""))
        # straight up wishing an empty one will have less than 3 databses
        if len(result.fetchall()) <= 3:
            return True
        else:
            return False

def initalize_databse_if_it_dont_exist(app):
    init_db_engine(app.config["POSTGRES_ADMIN_URI"])

    new = new_database_needed()
    if new:
        app.logger.info("CREATING A NEW ONE")
        exec_sql_script(os.path.join(SQL_DIR, "create_db.sql"))

        init_db_engine(app.config["SQLALCHEMY_URI"])

        app.logger.info("CREATE TABLES")
        exec_sql_script(os.path.join(SQL_DIR, "create_table.sql"))
        app.logger.info("DONE")