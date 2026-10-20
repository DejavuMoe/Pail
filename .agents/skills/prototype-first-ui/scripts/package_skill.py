#!/usr/bin/env python3
"""Create a deterministic, validated Agent Skill ZIP."""

from __future__ import annotations

import argparse
import hashlib
from pathlib import Path
import re
import stat
import sys
from typing import Optional, Tuple
import zipfile

from validate_skill import files_under, parse_frontmatter, print_validation, validate_directory, validate_zip

EXCLUDED_PARTS = {".git", "__pycache__", ".pytest_cache", ".mypy_cache", "node_modules"}
EXCLUDED_NAMES = {".DS_Store", "Thumbs.db", "desktop.ini"}
FIXED_TIMESTAMP = (2024, 1, 1, 0, 0, 0)


def release_metadata(source: Path, tag: Optional[str] = None) -> Tuple[str, str]:
    frontmatter, _ = parse_frontmatter((source / "SKILL.md").read_text(encoding="utf-8"))
    version = frontmatter.get("metadata", {}).get("version", "")
    if not re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", version):
        raise ValueError("metadata.version must be a stable SemVer version, e.g. 2.1.0")
    if tag is not None and tag != "v" + version:
        raise ValueError("Release tag %r must match metadata.version: v%s" % (tag, version))
    if frontmatter.get("license") != "MIT" or not (source / "LICENSE").read_text(encoding="utf-8").startswith("MIT License\n"):
        raise ValueError("SKILL.md and LICENSE must both declare MIT")
    sections = re.split(r"(?m)^## ", (source / "CHANGELOG.md").read_text(encoding="utf-8"))
    if len(sections) < 2 or not re.match(re.escape(version) + r" — \d{4}-\d{2}-\d{2}\n", sections[1]):
        raise ValueError("The first CHANGELOG.md release must match metadata.version and include a date")
    if not sections[1].partition("\n")[2].strip():
        raise ValueError("The current changelog entry must contain release notes")
    return version, "## " + sections[1].strip() + "\n"


def package(source: Path, output: Path, tag: Optional[str] = None) -> int:
    source = source.resolve()
    output = output.resolve()
    if output == source or source in output.parents:
        print("ERROR: Package output must be outside the source directory", file=sys.stderr)
        return 1
    validation = validate_directory(source)
    if print_validation(validation) != 0:
        return 1
    try:
        version, notes = release_metadata(source, tag)
    except (OSError, ValueError) as exc:
        print("ERROR: %s" % exc, file=sys.stderr)
        return 1

    paths = []
    for path in files_under(source):
        if not path.is_file() and not path.is_symlink():
            continue
        relative = path.relative_to(source)
        if path.name in EXCLUDED_NAMES or path.suffix == ".pyc" or any(part in EXCLUDED_PARTS for part in relative.parts):
            continue
        if path.is_symlink():
            print("ERROR: Refusing to package symlink: %s" % relative, file=sys.stderr)
            return 1
        paths.append(path)
    paths.sort(key=lambda value: value.relative_to(source).as_posix().casefold())

    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = output.with_name(output.name + ".tmp")
    temporary.unlink(missing_ok=True)
    with zipfile.ZipFile(temporary, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for path in paths:
            relative = Path(source.name) / path.relative_to(source)
            info = zipfile.ZipInfo(relative.as_posix(), date_time=FIXED_TIMESTAMP)
            info.compress_type = zipfile.ZIP_DEFLATED
            mode = path.stat().st_mode
            permissions = 0o755 if mode & stat.S_IXUSR else 0o644
            info.external_attr = (stat.S_IFREG | permissions) << 16
            info.create_system = 3
            archive.writestr(info, path.read_bytes(), compress_type=zipfile.ZIP_DEFLATED, compresslevel=9)
    temporary.replace(output)

    zip_validation = validate_zip(output)
    if print_validation(zip_validation) != 0:
        output.unlink(missing_ok=True)
        return 1
    digest = hashlib.sha256(output.read_bytes()).hexdigest()
    checksum_path = output.with_suffix(output.suffix + ".sha256")
    checksum_path.write_text("%s  %s\n" % (digest, output.name), encoding="ascii")
    output.with_suffix(".notes.md").write_text(notes, encoding="utf-8")
    print("Version: %s" % version)
    print("Packaged: %s" % output)
    print("Checksum: %s" % checksum_path)
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--tag", help="Require this exact vMAJOR.MINOR.PATCH release tag")
    return parser


def main() -> int:
    args = build_parser().parse_args()
    return package(Path(args.source), Path(args.output), args.tag)


if __name__ == "__main__":
    raise SystemExit(main())
