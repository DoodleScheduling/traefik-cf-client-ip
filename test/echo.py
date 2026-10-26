"""Backend for the integration test: answers with the client IP headers it received."""
import http.server
import json
import sys


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        names = ['X-Forwarded-For', 'X-Real-Ip', 'Cf-Connecting-Ip']
        body = json.dumps({n: self.headers.get_all(n) for n in names}, sort_keys=True).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


http.server.HTTPServer(('127.0.0.1', int(sys.argv[1])), Handler).serve_forever()
