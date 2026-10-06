#!/usr/bin/env python3
"""Validate SDK release versions and the actual archives sent to registries."""

import argparse
import io
import json
from pathlib import Path, PurePosixPath
import posixpath
import re
import tarfile
import xml.etree.ElementTree as ET
import zipfile

ROOT = Path(__file__).resolve().parent.parent
VERSION_PATTERN = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)-sdk\.[1-9][0-9]*")
PACKAGES = {"typescript": "@iapstack/host", "react-native": "@iapstack/react-native"}
CLIENT_FILES = (
    "sdk/go/client.go",
    "sdk/typescript/src/client.ts",
    "sdk/android/core/src/main/kotlin/com/iapstack/core/IapStackClient.kt",
    "sdk/ios/Sources/IAPStackApple/IAPStackClient.swift",
    "sdk/react-native/src/client.ts",
)
RUNTIME_DEPENDENCY = re.compile(r"""^\s*(?:api|implementation)\s*\(?\s*['"]([\w.-]+):[\w.-]+:([\w.-]+)['"]""", re.M)


def dependency_versions(text):
    """Map each versioned runtime dependency group in a Gradle build to its versions."""
    versions = {}
    for group, version in RUNTIME_DEPENDENCY.findall(text):
        versions.setdefault(group, set()).add(version)
    return versions


def check_metadata(root=ROOT):
    """Reject accidental stable releases, mismatched versions, or private packages."""
    version = (root / "sdk/version.txt").read_text().strip()
    if not VERSION_PATTERN.fullmatch(version):
        raise ValueError("SDK version must be a numbered SDK prerelease, e.g. 0.1.0-sdk.1")
    for folder, name in PACKAGES.items():
        package = json.loads((root / "sdk" / folder / "package.json").read_text())
        if package["name"] != name or package["version"] != version or package.get("private"):
            raise ValueError(f"{folder}: package name, version, or visibility mismatch")
        config = package.get("publishConfig", {})
        if config.get("access") != "public" or config.get("tag") != "next":
            raise ValueError(f"{folder}: prereleases must use public access and next dist-tag")
    if not (root / "Package.swift").is_file():
        raise ValueError("Swift distribution requires a root Package.swift")
    for path in CLIENT_FILES:
        if not re.search(r"(?:sdkVersion|SDK_VERSION)\s*=\s*['\"]" + re.escape(version) + r"['\"]", (root / path).read_text()):
            raise ValueError(f"{path}: X-IAPStack-SDK version differs from sdk/version.txt")
    # The React Native module compiles the canonical Android sources, so it must link the same libraries.
    canonical = {}
    for module in ("core", "google-play", "huawei"):
        for group, versions in dependency_versions((root / f"sdk/android/{module}/build.gradle.kts").read_text()).items():
            canonical.setdefault(group, set()).update(versions)
    bridge = dependency_versions((root / "sdk/react-native/android/build.gradle").read_text())
    for group, versions in canonical.items():
        if bridge.get(group) != versions:
            raise ValueError(f"sdk/react-native/android/build.gradle: {group} versions differ from the Android SDK")
    return version


def check_tarball(path, expected_name, version):
    """Check packed entrypoints and reject secrets, build caches, links and traversal."""
    with tarfile.open(path, "r:gz") as archive:
        members = archive.getmembers()
        files = {item.name: item for item in members if item.isfile()}
        for item in members:
            parts = PurePosixPath(item.name).parts
            if not parts or parts[0] != "package" or ".." in parts or item.issym() or item.islnk():
                raise ValueError(f"Unsafe archive entry: {item.name}")
            if any(part in {".git", "node_modules", ".gradle", ".build", "Pods"} for part in parts):
                raise ValueError(f"Build cache in package: {item.name}")
            if any(part == ".env" or part.startswith(".env.") or part.endswith((".jks", ".keystore", ".p12", ".mobileprovision")) for part in parts):
                raise ValueError(f"Local configuration in package: {item.name}")
        metadata_file = archive.extractfile("package/package.json")
        package = json.load(metadata_file)
        if package.get("name") != expected_name or package.get("version") != version:
            raise ValueError(f"Wrong package/version in {path}")
        for field in ("main", "types"):
            entry = package.get(field, "").removeprefix("./")
            if not entry or f"package/{entry}" not in files:
                raise ValueError(f"Missing {field} entrypoint in {path}")
        def check_export(value):
            if isinstance(value, str) and value.startswith("./"):
                if f"package/{value[2:]}" not in files:
                    raise ValueError(f"Missing exported entrypoint {value} in {path}")
            elif isinstance(value, dict):
                for target in value.values():
                    check_export(target)
            elif isinstance(value, list):
                for target in value:
                    check_export(target)
        check_export(package.get("exports", {}))
        if "react-native" in package:
            check_export(package["react-native"])
        if "package/LICENSE" not in files:
            raise ValueError(f"Missing Apache license in {path}")
        if expected_name.endswith("react-native"):
            # Metro before React Native 0.79 ignores exports; a nested manifest keeps subpaths resolvable.
            for subpath in package.get("exports", {}):
                if subpath in {".", "./package.json"}:
                    continue
                redirect = f"package/{subpath[2:]}/package.json"
                if redirect not in files:
                    raise ValueError(f"Missing legacy Metro redirect for {subpath} in {path}")
                for field, target in json.load(archive.extractfile(redirect)).items():
                    if field in {"main", "types", "react-native"}:
                        resolved = posixpath.normpath(f"{posixpath.dirname(redirect)}/{target}")
                        if resolved not in files:
                            raise ValueError(f"Legacy Metro redirect {redirect} points at missing {target}")
            if not any(name.endswith(".podspec") for name in files):
                raise ValueError("React Native package is missing its podspec")
            if not any(name.endswith(".swift") for name in files) or not any(name.endswith(".kt") for name in files):
                raise ValueError("React Native package is missing native companion sources")
            if not any(name.endswith(("android/build.gradle", "android/build.gradle.kts")) for name in files):
                raise ValueError("React Native package is missing its Android build")
            snapshots = [(ROOT / "sdk/ios/Sources/IAPStackApple", "package/native/ios")]
            snapshots += [(ROOT / f"sdk/android/{module}/src/main/kotlin", f"package/native/android/{module}") for module in ("core", "google-play", "huawei")]
            for source, destination in snapshots:
                for original in source.rglob("*"):
                    if original.is_file():
                        packed = f"{destination}/{original.relative_to(source).as_posix()}"
                        if packed not in files or archive.extractfile(packed).read() != original.read_bytes():
                            raise ValueError(f"React Native canonical source missing or stale: {packed}")


def check_maven(directory, version):
    """Verify all three Maven artifacts, source jars, coordinates and core dependencies."""
    namespace = {"m": "http://maven.apache.org/POM/4.0.0"}
    group = "io.github.imariman.iapstack"
    for module in ("core", "google-play", "huawei"):
        artifact = f"iapstack-{module}"
        base = Path(directory) / group.replace(".", "/") / artifact / version
        stem = f"{artifact}-{version}"
        pom = ET.parse(base / f"{stem}.pom").getroot()
        for key, expected in (("groupId", group), ("artifactId", artifact), ("version", version)):
            if pom.findtext(f"m:{key}", namespaces=namespace) != expected:
                raise ValueError(f"Wrong {key} in {artifact} POM")
        extension = "jar" if module == "core" else "aar"
        with zipfile.ZipFile(base / f"{stem}.{extension}") as archive:
            names = archive.namelist()
            if module == "core" and not any(name.endswith("IapStackClient.class") for name in names):
                raise ValueError("Core jar contains no IapStackClient")
            if module != "core":
                if "classes.jar" not in names or "AndroidManifest.xml" not in names:
                    raise ValueError(f"{artifact} AAR lacks classes or Android manifest")
                expected_class = "GooglePlayIapStack.class" if module == "google-play" else "HuaweiIapStack.class"
                with zipfile.ZipFile(io.BytesIO(archive.read("classes.jar"))) as classes:
                    if not any(name.endswith(expected_class) for name in classes.namelist()):
                        raise ValueError(f"{artifact} AAR lacks its native companion")
        with zipfile.ZipFile(base / f"{stem}-sources.jar") as archive:
            if not any(name.endswith(".kt") for name in archive.namelist()):
                raise ValueError(f"Missing Kotlin sources in {artifact}")
        if module != "core":
            dependencies = pom.findall("m:dependencies/m:dependency", namespace)
            if not any(
                item.findtext("m:groupId", namespaces=namespace) == group
                and item.findtext("m:artifactId", namespaces=namespace) == "iapstack-core"
                and item.findtext("m:version", namespaces=namespace) == version
                for item in dependencies
            ):
                raise ValueError(f"{artifact} lacks the matching core dependency")


def main():
    """Validate metadata, then optional staged npm and Maven outputs."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--npm-dir", type=Path)
    parser.add_argument("--maven-dir", type=Path)
    args = parser.parse_args()
    version = check_metadata()
    if args.npm_dir:
        for name in PACKAGES.values():
            filename = f"{name.removeprefix('@').replace('/', '-')}-{version}.tgz"
            check_tarball(args.npm_dir / filename, name, version)
    if args.maven_dir:
        check_maven(args.maven_dir, version)
    print(f"SDK {version}: package checks passed")


if __name__ == "__main__":
    main()
