"""Regression tests for SDK release input and artifact validation."""

import http.server
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import threading
import unittest
import xml.etree.ElementTree as ET
import zipfile


def load_script(filename):
    spec = importlib.util.spec_from_file_location(filename, Path(__file__).resolve().parents[1] / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


checks = load_script("check-sdk-packages.py")
maven = load_script("publish-sdk-maven.py")


class SDKPackageTests(unittest.TestCase):
    def check_archive(self, extra=None, missing=None, exports=None):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "host.tgz"
            files = {
                "package/package.json": json.dumps({"name": "@iapstack/host", "version": "0.1.0-sdk.1", "main": "./dist/index.js", "types": "./dist/index.d.ts"}),
                "package/dist/index.js": "export {};",
                "package/dist/index.d.ts": "export {};",
                "package/LICENSE": "Apache-2.0",
            }
            if exports:
                package = json.loads(files["package/package.json"])
                package["exports"] = exports
                files["package/package.json"] = json.dumps(package)
            if extra:
                files[extra] = "local-only-data"
            if missing:
                del files[missing]
            with tarfile.open(path, "w:gz") as archive:
                for name, content in files.items():
                    payload = content.encode()
                    member = tarfile.TarInfo(name)
                    member.size = len(payload)
                    archive.addfile(member, io.BytesIO(payload))
            checks.check_tarball(path, "@iapstack/host", "0.1.0-sdk.1")

    def test_valid_built_archive(self):
        self.check_archive()

    def test_missing_compiled_entrypoint_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "Missing main"):
            self.check_archive(missing="package/dist/index.js")

    def test_missing_conditional_export_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "Missing exported"):
            self.check_archive(exports={"./store": {"types": "./dist/missing.d.ts"}})

    def test_local_secret_files_and_traversal_are_rejected(self):
        for name in ("package/.env", "package/example/.env.local", "package/keys/upload.jks", "package/../../outside", "package/node_modules/cache"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.check_archive(extra=name)

    def test_stable_server_and_invalid_versions_are_rejected(self):
        for value in ("v0.1.0", "0.1.0", "0.1.0-alpha.1", "0.1.0-sdk.0", "01.1.0-sdk.1", "0.1.0-sdk.01"):
            self.assertIsNone(checks.VERSION_PATTERN.fullmatch(value))

    def check_flutter_tree(self, edit=None):
        version = "0.1.0-sdk.2"
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "LICENSE").write_text("Apache-2.0")
            for name in checks.FLUTTER_PACKAGES:
                folder = root / "sdk/flutter" / name
                folder.mkdir(parents=True)
                core = "" if name == "iapstack" else f"  iapstack: {version}\n"
                (folder / "pubspec.yaml").write_text(
                    f"name: {name}\nversion: {version}\n\nenvironment:\n  sdk: '>=3.4.0 <4.0.0'\n\n"
                    f"dependencies:\n  # Runtime dependencies.\n{core}  http: ^1.5.0\n\n"
                    "dev_dependencies:\n  test:\n    path: ../test\n"
                )
                if name != "iapstack":
                    example = folder / "example"
                    example.mkdir()
                    (example / "pubspec.yaml").write_text(
                        f"name: {name}_example\npublish_to: none\n\n"
                        f"dependencies:\n  iapstack: {version}\n  {name}:\n    path: ..\n"
                    )
                    (folder / ".pubignore").write_text("/example/pubspec_overrides.yaml\n")
                (folder / "LICENSE").write_text("Apache-2.0")
                (folder / "CHANGELOG.md").write_text(f"# Changelog\n\n## {version}\n\n- Initial release.\n")
            if edit:
                edit(root)
            checks.check_flutter(root, version)

    def test_valid_flutter_manifests_allow_dev_path_dependencies(self):
        self.check_flutter_tree()

    def test_unpublishable_flutter_manifests_are_rejected(self):
        def rewrite(name, old, new, file="pubspec.yaml"):
            def edit(root):
                path = root / "sdk/flutter" / name / file
                path.write_text(path.read_text().replace(old, new))
            return edit

        cases = {
            "version differs": rewrite("iapstack", "version: 0.1.0-sdk.2", "version: 0.1.0-dev.1"),
            "publish_to": rewrite("iapstack_huawei", "version:", "publish_to: none\nversion:"),
            "path and git": rewrite("iapstack_apple", "  iapstack: 0.1.0-sdk.2\n", "  iapstack:\n    path: ../iapstack\n"),
            "exact iapstack": rewrite("iapstack_google_play", "iapstack: 0.1.0-sdk.2", "iapstack: ^0.1.0-sdk.2"),
            "iapstack_apple/example: must depend on the exact iapstack": rewrite(
                "iapstack_apple",
                "  iapstack: 0.1.0-sdk.2\n",
                "  iapstack:\n    path: ../../iapstack\n",
                file="example/pubspec.yaml",
            ),
            "iapstack_huawei/example: must depend on the exact iapstack": rewrite(
                "iapstack_huawei",
                "iapstack: 0.1.0-sdk.2",
                "iapstack: 0.1.0-sdk.1",
                file="example/pubspec.yaml",
            ),
            r"iapstack_google_play: \.pubignore must exclude": rewrite(
                "iapstack_google_play",
                "/example/pubspec_overrides.yaml\n",
                "",
                file=".pubignore",
            ),
            "LICENSE": rewrite("iapstack", "Apache-2.0", "MIT", file="LICENSE"),
            "CHANGELOG": rewrite("iapstack_apple", "## 0.1.0-sdk.2", "## 0.1.0-sdk.1", file="CHANGELOG.md"),
        }
        for message, edit in cases.items():
            with self.subTest(message), self.assertRaisesRegex(ValueError, message):
                self.check_flutter_tree(edit)

    def test_missing_companion_pubignore_is_rejected(self):
        with self.assertRaisesRegex(ValueError, r"\.pubignore must exclude"):
            self.check_flutter_tree(lambda root: (root / "sdk/flutter/iapstack_apple/.pubignore").unlink())

    def test_runtime_dependency_versions_ignore_test_and_unversioned_libraries(self):
        groovy = "implementation 'com.facebook.react:react-android'\nimplementation 'com.squareup.okhttp3:okhttp:4.12.0'\ntestImplementation 'com.squareup.okhttp3:mockwebserver:5.0.0'\n"
        kotlin = '  api("com.squareup.okhttp3:okhttp:4.12.0")\n  implementation("com.huawei.hms:iap:6.13.0.300")\n'
        self.assertEqual(checks.dependency_versions(groovy), {"com.squareup.okhttp3": {"4.12.0"}})
        self.assertEqual(checks.dependency_versions(kotlin), {"com.squareup.okhttp3": {"4.12.0"}, "com.huawei.hms": {"6.13.0.300"}})

    def test_maven_rerun_keeps_history_and_latest(self):
        original = maven.merged_metadata(None, "iapstack-core", "0.1.0-sdk.10")
        retried = maven.merged_metadata(original, "iapstack-core", "0.1.0-sdk.2")
        root = ET.fromstring(retried)
        self.assertEqual(root.findtext("versioning/latest"), "0.1.0-sdk.10")
        self.assertEqual([node.text for node in root.findall("versioning/versions/version")], ["0.1.0-sdk.10", "0.1.0-sdk.2"])
        again = ET.fromstring(maven.merged_metadata(retried, "iapstack-core", "0.1.0-sdk.2"))
        self.assertEqual(len(again.findall("versioning/versions/version")), 2)

    def test_maven_download_redirect_drops_credentials(self):
        seen = {}

        class Blob(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                seen["blob"] = self.headers.get("Authorization")
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b"published bytes")

            def log_message(self, *args):
                pass

        blob = http.server.HTTPServer(("127.0.0.1", 0), Blob)

        class Registry(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                seen["registry"] = self.headers.get("Authorization")
                if self.path.endswith("missing.pom"):
                    self.send_response(404)
                else:
                    self.send_response(302)
                    self.send_header("Location", f"http://127.0.0.1:{blob.server_port}/signed")
                self.end_headers()

            def log_message(self, *args):
                pass

        registry = http.server.HTTPServer(("127.0.0.1", 0), Registry)
        servers = [blob, registry]
        for server in servers:
            threading.Thread(target=server.serve_forever, daemon=True).start()
        original = maven.REGISTRY
        maven.REGISTRY = f"http://127.0.0.1:{registry.server_port}/"
        try:
            self.assertEqual(maven.request("GET", "artifact.aar", "Basic secret"), b"published bytes")
            self.assertEqual(seen, {"registry": "Basic secret", "blob": None})
            self.assertIsNone(maven.request("GET", "missing.pom", "Basic secret"))
        finally:
            maven.REGISTRY = original
            for server in servers:
                server.shutdown()
                server.server_close()

    def test_maven_coordinate_mismatch_is_rejected(self):
        original = maven.merged_metadata(None, "iapstack-core", "0.1.0-sdk.1")
        with self.assertRaisesRegex(ValueError, "coordinate mismatch"):
            maven.merged_metadata(original, "iapstack-huawei", "0.1.0-sdk.1")

    def test_incomplete_maven_aar_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            version = "0.1.0-sdk.1"
            for module, extension in (("core", "jar"), ("google-play", "aar")):
                artifact = f"iapstack-{module}"
                base = Path(directory) / "io/github/imariman/iapstack" / artifact / version
                base.mkdir(parents=True)
                stem = f"{artifact}-{version}"
                (base / f"{stem}.pom").write_text(f'<project xmlns="http://maven.apache.org/POM/4.0.0"><groupId>io.github.imariman.iapstack</groupId><artifactId>{artifact}</artifactId><version>{version}</version></project>')
                with zipfile.ZipFile(base / f"{stem}-sources.jar", "w") as archive:
                    archive.writestr("Example.kt", "// fixture")
                with zipfile.ZipFile(base / f"{stem}.{extension}", "w") as archive:
                    archive.writestr("IapStackClient.class" if module == "core" else "classes.jar", "not a nested jar")
            with self.assertRaisesRegex(ValueError, "manifest"):
                checks.check_maven(directory, version)

    def test_npm_resume_cannot_move_next_backwards(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            version = (Path(__file__).resolve().parents[2] / "sdk/version.txt").read_text().strip()
            prefix, counter = version.rsplit(".", 1)
            next_version = f"{prefix}.{int(counter) + 1}"
            (base / f"iapstack-host-{version}.tgz").write_bytes(b"fixture archive")
            npm = base / "npm"
            npm.write_text('''#!/usr/bin/env python3
import json, os, sys
if sys.argv[1] == 'publish':
    raise RuntimeError('Must not publish an older version')
if 'dist.integrity' in sys.argv:
    print(json.dumps({'error': {'code': 'E404'}}))
    sys.exit(1)
print(json.dumps(os.environ['IAPSTACK_TEST_NEXT_VERSION']))
''')
            npm.chmod(0o755)
            script = Path(__file__).resolve().parents[1] / "publish-sdk-npm.mjs"
            result = subprocess.run(["node", str(script), directory], env={**os.environ, "PATH": f"{directory}:{os.environ['PATH']}", "IAPSTACK_TEST_NEXT_VERSION": next_version}, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Refusing to move", result.stderr)


if __name__ == "__main__":
    unittest.main()
