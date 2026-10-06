#!/usr/bin/env python3
"""Upload the verified Maven staging bytes, safely resuming partial releases."""

import base64
from datetime import datetime, timezone
import hashlib
import importlib.util
import os
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parent.parent
REGISTRY = "https://maven.pkg.github.com/imariman/iapstack/"
GROUP_PATH = "io/github/imariman/iapstack"


def request(method, path, authorization, body=None):
    """Exchange an artifact without exposing credentials to redirect targets."""
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None

    registry = urllib.parse.urlsplit(REGISTRY)
    url = REGISTRY + path
    for _ in range(4):
        target = urllib.parse.urlsplit(url)
        # GitHub Packages serves downloads from pre-signed blob URLs on another host.
        headers = {} if body is None else {"Content-Type": "application/octet-stream"}
        if (target.scheme, target.netloc) == (registry.scheme, registry.netloc):
            headers["Authorization"] = authorization
        req = urllib.request.Request(url, data=body, headers=headers, method=method)
        try:
            with urllib.request.build_opener(NoRedirect()).open(req, timeout=60) as response:
                return response.read()
        except urllib.error.HTTPError as error:
            location = error.headers.get("Location")
            error.close()
            if method == "GET" and error.code in (301, 302, 303, 307, 308) and location:
                url = urllib.parse.urljoin(url, location)
                if urllib.parse.urlsplit(url).scheme != registry.scheme:
                    raise RuntimeError(f"Maven GET redirect changed scheme for {path}") from None
                continue
            if method == "GET" and error.code == 404:
                return None
            raise RuntimeError(f"Maven {method} failed with HTTP {error.code} for {path}") from None
    raise RuntimeError(f"Maven GET redirected too many times for {path}")


def merged_metadata(existing, artifact, version):
    """Preserve old releases and never move latest backwards on an older rerun."""
    root = ET.fromstring(existing) if existing else ET.Element("metadata")
    for field, value in (("groupId", "io.github.imariman.iapstack"), ("artifactId", artifact)):
        node = root.find(field)
        if node is None:
            node = ET.SubElement(root, field)
        elif node.text != value:
            raise ValueError("Maven metadata coordinate mismatch")
        node.text = value
    versioning = root.find("versioning")
    if versioning is None:
        versioning = ET.SubElement(root, "versioning")
    versions = versioning.find("versions")
    if versions is None:
        versions = ET.SubElement(versioning, "versions")
    values = [node.text for node in versions.findall("version")]
    if version not in values:
        ET.SubElement(versions, "version").text = version
        values.append(version)
    # This publication train deliberately accepts only X.Y.Z-sdk.N versions.
    def order(value):
        import re
        match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)-sdk\.(\d+)", value)
        if not match:
            raise ValueError("Unknown Maven version: inspect metadata before changing release policy")
        return tuple(map(int, match.groups()))
    latest = max(values, key=order)
    for field, value in (("latest", latest), ("release", latest), ("lastUpdated", datetime.now(timezone.utc).strftime("%Y%m%d%H%M%S"))):
        node = versioning.find(field)
        if node is None:
            node = ET.SubElement(versioning, field)
        node.text = value
    return ET.tostring(root, encoding="utf-8", xml_declaration=True)


def main():
    """Validate staging and upload immutable versioned artifacts before metadata."""
    spec = importlib.util.spec_from_file_location("sdk_checks", ROOT / "scripts/check-sdk-packages.py")
    checks = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(checks)
    version = checks.check_metadata()
    staging = ROOT / "sdk/android/build/maven"
    checks.check_maven(staging, version)
    username, token = os.environ.get("GITHUB_ACTOR"), os.environ.get("GITHUB_TOKEN")
    if not username or not token:
        raise ValueError("GITHUB_ACTOR and GITHUB_TOKEN are required for Maven publication")
    authorization = "Basic " + base64.b64encode(f"{username}:{token}".encode()).decode()
    for module in ("core", "google-play", "huawei"):
        artifact = f"iapstack-{module}"
        directory = staging / GROUP_PATH / artifact / version
        for path in sorted(directory.iterdir()):
            if not path.is_file():
                continue
            relative = path.relative_to(staging).as_posix()
            body = path.read_bytes()
            existing = request("GET", relative, authorization)
            if existing is not None and existing != body:
                raise ValueError(f"Published artifact differs; refusing overwrite: {relative}")
            if existing is None:
                request("PUT", relative, authorization, body)
        metadata_path = f"{GROUP_PATH}/{artifact}/maven-metadata.xml"
        metadata = merged_metadata(request("GET", metadata_path, authorization), artifact, version)
        request("PUT", metadata_path, authorization, metadata)
        for algorithm in ("sha1", "sha256", "sha512", "md5"):
            digest = hashlib.new(algorithm, metadata).hexdigest().encode()
            request("PUT", f"{metadata_path}.{algorithm}", authorization, digest)
        print(f"{artifact}:{version}: published verified staging artifacts")


if __name__ == "__main__":
    main()
