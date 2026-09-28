"""Single-tester host example. Deploy behind HTTPS; never run inside the Android app."""
import hmac
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import quote, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def mint_session(config):
    base_url = config["IAPSTACK_BASE_URL"].rstrip("/")
    parsed = urlsplit(base_url)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.query or parsed.fragment:
        raise ValueError("IAPSTACK_BASE_URL must use HTTPS")
    application_id = config["IAPSTACK_APPLICATION_ID"]
    customer = config["EXAMPLE_EXTERNAL_CUSTOMER_ID"]
    # Customer identity is selected by the host, never taken from the request body.
    request = Request(
        f"{base_url}/v1/applications/{quote(application_id, safe='')}/customer-sessions",
        data=json.dumps({"external_customer_id": customer}).encode(),
        headers={"Authorization": f"Bearer {config['IAPSTACK_APPLICATION_TOKEN']}",
                 "Content-Type": "application/json"},
        method="POST",
    )
    with build_opener(NoRedirect()).open(request, timeout=15) as response:
        payload = response.read(65537)
        if len(payload) > 65536:
            raise ValueError("Oversized session response")
        session = json.loads(payload)
    return {"base_url": base_url, "application_id": application_id,
            "external_customer_id": customer, "token": session["token"],
            "expires_at": session["expires_at"]}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass  # Never log authentication headers or session bodies.

    def do_POST(self):
        expected = os.environ["EXAMPLE_LOGIN_TOKEN"]
        authenticated = hmac.compare_digest(
            self.headers.get("Authorization", "").encode(), f"Bearer {expected}".encode())
        if self.path != "/session" or not authenticated:
            self.reply(401, {"error": "unauthorized"})
            return
        try:
            session = mint_session(os.environ)
        except Exception:
            self.reply(502, {"error": "session_unavailable"})
            return
        self.reply(200, session)

    def reply(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    for name in ("IAPSTACK_BASE_URL", "IAPSTACK_APPLICATION_ID", "IAPSTACK_APPLICATION_TOKEN",
                 "EXAMPLE_EXTERNAL_CUSTOMER_ID", "EXAMPLE_LOGIN_TOKEN"):
        if not os.environ.get(name):
            raise SystemExit(f"Missing {name}")
    if os.environ["EXAMPLE_LOGIN_TOKEN"] == os.environ["IAPSTACK_APPLICATION_TOKEN"]:
        raise SystemExit("Use a separate tester login token, never the application bearer")
    ThreadingHTTPServer(("127.0.0.1", 8099), Handler).serve_forever()
