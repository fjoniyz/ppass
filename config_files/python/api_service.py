import json
import os
import time
from http.server import HTTPServer, BaseHTTPRequestHandler
import psycopg2
import psycopg2.extras

DB_HOST = os.environ.get("DB_HOST", "10.0.0.2")
DB_PORT = int(os.environ.get("DB_PORT", "5432"))
DB_NAME = os.environ.get("DB_NAME", "postgres")
DB_USER = os.environ.get("DB_USER", "postgres")
DB_PASSWORD = os.environ.get("DB_PASSWORD", "postgrespassword")

def execute_db_query():
    """
    Connects to the database, creates a sample table if needed,
    and runs a SELECT * query.
    """
    try:
        conn = psycopg2.connect(
            host=DB_HOST,
            port=DB_PORT,
            dbname=DB_NAME,
            user=DB_USER,
            password=DB_PASSWORD,
            connect_timeout=3,
        )
        conn.autocommit = True
        with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
            cur.execute("""
                CREATE TABLE IF NOT EXISTS users (
                    id SERIAL PRIMARY KEY,
                    username VARCHAR(50) NOT NULL,
                    email VARCHAR(100) NOT NULL,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                );
            """)
            cur.execute("SELECT COUNT(*) AS count FROM users;")
            res = cur.fetchone()
            if res and res["count"] == 0:
                cur.execute("""
                    INSERT INTO users (username, email) VALUES
                        ('alice', 'alice@example.local'),
                        ('bob', 'bob@example.local'),
                        ('charlie', 'charlie@example.local');
                """)

            cur.execute("SELECT * FROM users;")
            rows = cur.fetchall()

            serialized_rows = []
            for row in rows:
                r = dict(row)
                for k, v in r.items():
                    if hasattr(v, "isoformat"):
                        r[k] = v.isoformat()
                serialized_rows.append(r)

            conn.close()
            return {
                "status": "connected",
                "host": f"{DB_HOST}:{DB_PORT}",
                "database": DB_NAME,
                "query": "SELECT * FROM users;",
                "rows": serialized_rows,
            }
    except Exception as e:
        return {
            "status": "error",
            "host": f"{DB_HOST}:{DB_PORT}",
            "database": DB_NAME,
            "query": "SELECT * FROM users;",
            "error": str(e),
        }

class APIHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()

        db_data = execute_db_query()

        response = {
            "service": "api-service",
            "status": "healthy",
            "message": "PaaS API Service with Database Querying",
            "pid": os.getpid(),
            "path": self.path,
            "timestamp": time.time(),
            "database": db_data,
        }
        self.wfile.write(json.dumps(response, indent=2).encode("utf-8") + b"\n")

    def log_message(self, format, *args):
        print(f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] {self.address_string()} - {format % args}")

if __name__ == "__main__":
    port = 8888
    server_address = ('', port)
    httpd = HTTPServer(server_address, APIHandler)
    print(f"Starting api-service on port {port}...")
    httpd.serve_forever()
