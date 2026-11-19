#!/usr/bin/env python3
"""Install one authenticated MCP Skill package locally; no network or business writes."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import sys
import tempfile
import uuid

FORMAT = "devops-workflow-skill/v1"
RECEIPT = ".platform-skill.json"
MAX_BYTES = 512 * 1024


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def safe_path(value):
    if not isinstance(value, str) or not value or "\\" in value or ":" in value:
        raise ValueError("Invalid relative file path")
    p = PurePosixPath(value)
    if p.is_absolute() or str(p) != value or any(x in (".", "..") or x.startswith(".") for x in p.parts):
        raise ValueError("Unsafe relative file path")
    return value


def validate_package(package):
    if package.get("format") != FORMAT:
        raise ValueError("Unsupported package format")
    ident, version = package.get("id"), package.get("version")
    if not isinstance(ident, str) or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", ident) or len(ident) > 63:
        raise ValueError("Invalid Skill ID")
    if not isinstance(version, str) or not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9.-]+)?", version):
        raise ValueError("Invalid version")
    files = package.get("files")
    if not isinstance(files, list) or not 1 <= len(files) <= 30:
        raise ValueError("Invalid file list")
    contents, hashes, size = {}, {}, 0
    for item in files:
        name = safe_path(item["path"])
        if name in contents or not isinstance(item["content"], str):
            raise ValueError("Duplicate path or non-text file")
        raw = item["content"].encode("utf-8")
        size += len(raw)
        if size > MAX_BYTES or sha(raw) != item["sha256"]:
            raise ValueError("File digest mismatch or package too large")
        contents[name], hashes[name] = raw, sha(raw)
    if "SKILL.md" not in contents:
        raise ValueError("Missing SKILL.md")
    manifest = FORMAT + "\n" + ident + "\n" + version + "\n"
    manifest += "".join(name + "\0" + hashes[name] + "\n" for name in sorted(hashes))
    if sha(manifest.encode("utf-8")) != package.get("sha256"):
        raise ValueError("Package digest mismatch")
    return contents, hashes


def assert_directory(path):
    if path.is_symlink() or (path.exists() and not path.is_dir()):
        raise ValueError("Directory is a symlink or not a directory: " + str(path))


def tree(path):
    assert_directory(path)
    result = {}
    if path.exists():
        for parent, dirs, files in os.walk(path, followlinks=False):
            for name in dirs + files:
                item = Path(parent) / name
                if item.is_symlink():
                    raise ValueError("Symlink inside installed Skill: " + str(item))
                if item.is_dir():
                    continue
                if not item.is_file() or item.stat().st_size > MAX_BYTES:
                    raise ValueError("Unsupported or oversized installed file")
                relative = item.relative_to(path).as_posix()
                if relative != RECEIPT:
                    result[relative] = sha(item.read_bytes())
    return result


def read_receipt(target, ident):
    file = target / RECEIPT
    if not file.exists():
        return None
    if file.is_symlink() or file.stat().st_size > MAX_BYTES:
        raise ValueError("Invalid local receipt")
    data = json.loads(file.read_text(encoding="utf-8"))
    if data.get("format") != FORMAT or data.get("id") != ident or not isinstance(data.get("files"), dict):
        raise ValueError("Invalid local receipt")
    for name, value in data["files"].items():
        safe_path(name)
        if not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{64}", value):
            raise ValueError("Invalid receipt digest")
    return data


def inspect(target, package, hashes):
    if not target.exists() and not target.is_symlink():
        return "install_available", {}, None
    current = tree(target)
    receipt = read_receipt(target, package["id"])
    if receipt is None:
        return ("adoption_required" if current == hashes else "conflict"), current, receipt
    if receipt["files"] != current:
        return "conflict", current, receipt
    if current == hashes and receipt.get("sha256") == package["sha256"] and receipt.get("version") == package["version"]:
        return "up_to_date", current, receipt
    return "update_available", current, receipt


def install(package, scope, project_root=None, apply=False, adopt=False, update_only=False):
    contents, hashes = validate_package(package)
    home = Path.home().resolve()
    if scope not in ("project", "user"):
        raise ValueError("Invalid scope")
    project = None
    if project_root is not None:
        raw = Path(project_root)
        if not raw.is_absolute() or not raw.is_dir():
            raise ValueError("Project root must be an existing absolute directory")
        project = raw.resolve()
    if scope == "project" and project is None:
        raise ValueError("Project scope requires --project-root")
    base = project if scope == "project" else home
    agents, skills = base / ".agents", base / ".agents/skills"
    assert_directory(agents)
    assert_directory(skills)
    target = skills / package["id"]
    other = (home if scope == "project" else project)
    if other is not None and other != base:
        other_target = other / ".agents/skills" / package["id"]
        # Detect the other scope without following it or deleting anything there.
        if other_target.exists() or other_target.is_symlink():
            return {"state": "conflict", "reason": "Skill already exists in another scope", "other_target": str(other_target), "verified": False}
    state, current, receipt = inspect(target, package, hashes)
    result = {"state": state, "id": package["id"], "version": package["version"], "sha256": package["sha256"], "scope": scope, "target": str(target), "verified": state == "up_to_date"}
    if update_only and state == "install_available":
        result.update(state="not_installed", verified=False)
        return result
    if state == "conflict":
        result["reason"] = "Existing files are unmanaged or locally modified; nothing was replaced"
    if not apply or state in ("up_to_date", "conflict"):
        return result
    if state == "adoption_required" and not adopt:
        return result

    agents.mkdir(mode=0o700, exist_ok=True)
    skills.mkdir(mode=0o700, exist_ok=True)
    lock = skills / (".platform-skill-" + package["id"] + ".lock")
    try:
        lock.mkdir(mode=0o700)
    except FileExistsError:
        raise ValueError("Another install/update may be active; no files changed")
    stage = None
    backup = None
    moved = False
    try:
        staging_root, backup_root = agents / "skill-staging", agents / "skill-backups"
        assert_directory(staging_root)
        assert_directory(backup_root)
        staging_root.mkdir(mode=0o700, exist_ok=True)
        stage = Path(tempfile.mkdtemp(prefix=package["id"] + "-", dir=staging_root))
        for name, raw in contents.items():
            file = stage / name
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_bytes(raw)
            file.chmod(0o644)
        manifest = {"format": FORMAT, "id": package["id"], "version": package["version"], "sha256": package["sha256"], "files": hashes}
        (stage / RECEIPT).write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        if tree(stage) != hashes:
            raise ValueError("Staged files failed verification")
        # Refuse to replace files modified after the first inspection.
        again, current_again, receipt_again = inspect(target, package, hashes)
        if (again, current_again, receipt_again) != (state, current, receipt):
            raise ValueError("Installed files changed during inspection; try check again")
        if state != "install_available":
            backup_root.mkdir(mode=0o700, exist_ok=True)
            backup = backup_root / (package["id"] + "-" + uuid.uuid4().hex)
            target.rename(backup)
            moved = True
        stage.rename(target)
        stage = None
        if tree(target) != hashes or read_receipt(target, package["id"]) != manifest:
            raise ValueError("Installed files failed verification")
        result.update(state={"install_available": "installed", "update_available": "updated", "adoption_required": "adopted"}[state], verified=True)
        if backup is not None:
            result["backup"] = str(backup)
        return result
    except Exception:
        # Recover the old clean installation after our new directory was moved.
        if moved and backup is not None and backup.exists():
            if target.exists():
                quarantine = agents / ("skill-failed-" + uuid.uuid4().hex)
                target.rename(quarantine)
            backup.rename(target)
        raise
    finally:
        if stage is not None:
            shutil.rmtree(stage)
        lock.rmdir()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--package", required=True)
    parser.add_argument("--scope", required=True, choices=("project", "user"))
    parser.add_argument("--project-root")
    parser.add_argument("--adopt", action="store_true", help="Adopt only a byte-identical manual installation after user consent")
    parser.add_argument("--update-only", action="store_true", help="Do not create a new installation")
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--check", action="store_true")
    mode.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    try:
        file = Path(args.package)
        if file.stat().st_size > 2 * MAX_BYTES:
            raise ValueError("Package JSON too large")
        package = json.loads(file.read_text(encoding="utf-8"))
        result = install(package, args.scope, args.project_root, args.apply, args.adopt, args.update_only)
        print(json.dumps(result, ensure_ascii=False))
        return 2 if result["state"] in ("conflict", "adoption_required") else 0
    except Exception as error:
        print(json.dumps({"state": "error", "verified": False, "error": str(error)}, ensure_ascii=False))
        return 1


if __name__ == "__main__":
    sys.exit(main())
