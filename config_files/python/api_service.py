import json
import os
import time
from http.server import HTTPServer, BaseHTTPRequestHandler

class APIHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()

        response = {
            "service": "api-service",
            "status": "healthy",
            "message": "Welcome to the PaaS API Service!",
            "pid": os.getpid(),
            "path": self.path,
            "timestamp": time.time(),
        }
        self.wfile.write(json.dumps(response, indent=2).encode("utf-8") + b"\n")

    def log_message(self, format, *args):
        # Clean logging format
        print(f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] {self.address_string()} - {format % args}")

if __name__ == "__main__":
    port = 8888
    server_address = ('', port)
    httpd = HTTPServer(server_address, APIHandler)
    print(f"Starting api-service on port {port}...")
    httpd.serve_forever()
