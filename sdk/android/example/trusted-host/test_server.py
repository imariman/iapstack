import io
import json
import unittest
from unittest.mock import patch

from server import Handler, mint_session


CONFIG = {
    "IAPSTACK_BASE_URL": "https://iap.example/api",
    "IAPSTACK_APPLICATION_ID": "app-id",
    "IAPSTACK_APPLICATION_TOKEN": "durable-host-only",
    "EXAMPLE_EXTERNAL_CUSTOMER_ID": "opaque-customer",
    "EXAMPLE_LOGIN_TOKEN": "separate-tester-login",
}


class HostTests(unittest.TestCase):
    def test_mints_only_host_selected_customer_and_returns_no_durable_secret(self):
        with patch("server.build_opener") as factory:
            factory.return_value.open.return_value.__enter__.return_value = io.BytesIO(
                b'{"token":"short-lived","expires_at":"2026-09-29T00:10:00Z"}')
            result = mint_session(CONFIG)
            request = factory.return_value.open.call_args.args[0]
        self.assertEqual(request.full_url, "https://iap.example/api/v1/applications/app-id/customer-sessions")
        self.assertEqual(json.loads(request.data), {"external_customer_id": "opaque-customer"})
        self.assertEqual(request.get_header("Authorization"), "Bearer durable-host-only")
        self.assertEqual(result["token"], "short-lived")
        self.assertNotIn("durable-host-only", json.dumps(result))

    def test_rejects_unauthenticated_request_without_minting(self):
        handler = object.__new__(Handler)
        handler.headers = {"Authorization": "Bearer incorrect"}
        handler.path = "/session"
        responses = []
        handler.reply = lambda status, payload: responses.append((status, payload))
        with patch.dict("os.environ", CONFIG), patch("server.mint_session") as mint:
            handler.do_POST()
            mint.assert_not_called()
        self.assertEqual(responses[0][0], 401)

    def test_rejects_http_and_userinfo_before_sending_bearer(self):
        for url in ("http://iap.example", "https://user:password@iap.example", "https://iap.example?secret=yes"):
            with self.subTest(url=url), patch("server.build_opener") as factory:
                with self.assertRaises(ValueError):
                    mint_session(dict(CONFIG, IAPSTACK_BASE_URL=url))
                factory.assert_not_called()


if __name__ == "__main__":
    unittest.main()
