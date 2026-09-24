#!/usr/bin/env python3
import json, os, sqlite3
from http.server import BaseHTTPRequestHandler, HTTPServer
DB=os.environ['QUAL_DB']
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == '/ready': body={'ready': True}
        elif self.path == '/version': body={'version': os.environ.get('QUAL_VERSION','1.0.0')}
        elif self.path == '/data':
            with sqlite3.connect(DB) as db: body={'value': db.execute('select value from fixture where id=1').fetchone()[0]}
        else: self.send_error(404); return
        raw=json.dumps(body).encode(); self.send_response(200); self.send_header('Content-Length',str(len(raw))); self.end_headers(); self.wfile.write(raw)
    def log_message(self, *_): pass
HTTPServer(('127.0.0.1',18080), Handler).serve_forever()
