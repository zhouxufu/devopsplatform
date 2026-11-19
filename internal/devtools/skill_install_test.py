"""Isolated filesystem tests; never touches the real user's Skill locations."""
import copy
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("installer", sys.argv[1])
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
BUNDLES = json.loads(Path(sys.argv[2]).read_text())
sys.argv = sys.argv[:1]


def revise(bundle, version="1.0.1"):
    b = copy.deepcopy(bundle)
    b["version"] = version
    b["files"][0]["content"] += "\nUpdated workflow guidance.\n"
    return seal(b)


def seal(b):
    for f in b["files"]:
        f["sha256"] = mod.sha(f["content"].encode())
    manifest = mod.FORMAT + "\n" + b["id"] + "\n" + b["version"] + "\n"
    manifest += "".join(f["path"] + "\0" + f["sha256"] + "\n" for f in sorted(b["files"], key=lambda f: f["path"]))
    b["sha256"] = mod.sha(manifest.encode())
    return b


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name).resolve()
        self.project, self.home = root / "project with spaces", root / "user"
        self.project.mkdir()
        self.home.mkdir()
        self.mock_home = patch.object(Path, "home", return_value=self.home)
        self.mock_home.start()
        self.addCleanup(self.mock_home.stop)
        self.bundle = copy.deepcopy(BUNDLES[0])
        self.target = self.project / ".agents/skills" / self.bundle["id"]

    def run_install(self, **kwargs):
        return mod.install(kwargs.pop("package", self.bundle), kwargs.pop("scope", "project"), str(self.project), **kwargs)

    def snapshot(self, root=None):
        return {p.relative_to(root or self.project).as_posix(): (p.read_bytes(), p.stat().st_mtime_ns) for p in (root or self.project).rglob("*") if p.is_file()}

    def test_check_and_update_only_never_create_directories(self):
        self.assertEqual(self.run_install()["state"], "install_available")
        self.assertFalse((self.project / ".agents").exists())
        self.assertEqual(self.run_install(apply=True, update_only=True)["state"], "not_installed")
        self.assertFalse((self.project / ".agents").exists())

    def test_install_all_packages_and_duplicate_is_noop(self):
        for bundle in BUNDLES:
            result = self.run_install(package=bundle, apply=True)
            self.assertEqual(result["state"], "installed")
            self.assertTrue(result["verified"])
        before = self.snapshot()
        result = self.run_install(apply=True)
        self.assertEqual(result["state"], "up_to_date")
        self.assertEqual(self.snapshot(), before)

    def test_update_backs_up_exact_old_tree_and_verifies_new(self):
        self.run_install(apply=True)
        old = self.snapshot(self.target)
        new = revise(self.bundle)
        self.assertEqual(self.run_install(package=new)["state"], "update_available")
        result = self.run_install(package=new, apply=True, update_only=True)
        self.assertEqual(result["state"], "updated")
        self.assertTrue(result["verified"])
        self.assertEqual(self.snapshot(Path(result["backup"])), old)
        self.assertEqual(mod.tree(self.target), {f["path"]: f["sha256"] for f in new["files"]})
        self.assertNotIn("skills/", Path(result["backup"]).relative_to(self.project / ".agents").as_posix())

    def test_local_edit_addition_and_deletion_block_update(self):
        self.run_install(apply=True)
        (self.target / "SKILL.md").write_text("my own guidance")
        (self.target / "custom.txt").write_text("keep me")
        (self.target / "agents/openai.yaml").unlink()
        before = self.snapshot()
        result = self.run_install(package=revise(self.bundle), apply=True)
        self.assertEqual(result["state"], "conflict")
        self.assertEqual(self.snapshot(), before)

    def test_manual_same_content_requires_explicit_adoption(self):
        self.run_install(apply=True)
        (self.target / mod.RECEIPT).unlink()
        before = self.snapshot()
        self.assertEqual(self.run_install(apply=True)["state"], "adoption_required")
        self.assertEqual(self.snapshot(), before)
        result = self.run_install(apply=True, adopt=True)
        self.assertEqual(result["state"], "adopted")
        self.assertTrue(result["verified"])
        self.assertFalse((Path(result["backup"]) / mod.RECEIPT).exists())

    def test_unknown_manual_content_cannot_be_adopted(self):
        self.target.mkdir(parents=True)
        (self.target / "SKILL.md").write_text("a different Skill")
        before = self.snapshot()
        self.assertEqual(self.run_install(apply=True, adopt=True)["state"], "conflict")
        self.assertEqual(self.snapshot(), before)

    def test_user_scope_and_duplicate_project_are_isolated(self):
        result = self.run_install(scope="user", apply=True)
        self.assertTrue(result["verified"])
        self.assertTrue(Path(result["target"]).is_relative_to(self.home))
        self.assertEqual(self.run_install(apply=True)["state"], "conflict")
        self.assertFalse((self.project / ".agents").exists())

    def test_tampering_and_traversal_fail_before_any_write(self):
        bad = copy.deepcopy(self.bundle)
        bad["files"][0]["content"] += "tampered"
        with self.assertRaises(ValueError):
            self.run_install(package=bad, apply=True)
        for name in ("../escape", "/tmp/escape", "agents/../../escape", "a\\escape", "./SKILL.md", ".platform-skill.json"):
            bad = copy.deepcopy(self.bundle)
            bad["files"][0]["path"] = name
            with self.assertRaises(ValueError):
                self.run_install(package=seal(bad), apply=True)
        self.assertFalse((self.project / ".agents").exists())

    def test_symlink_target_parent_and_installed_file_are_rejected(self):
        (self.project / ".agents").symlink_to(self.home, target_is_directory=True)
        with self.assertRaises(ValueError):
            self.run_install(apply=True)
        (self.project / ".agents").unlink()
        self.run_install(apply=True)
        file = self.target / "SKILL.md"
        file.unlink()
        sentinel = self.home / "keep"
        sentinel.write_text("outside")
        file.symlink_to(sentinel)
        with self.assertRaises(ValueError):
            self.run_install(package=revise(self.bundle), apply=True)
        self.assertEqual(sentinel.read_text(), "outside")

    def test_lock_prevents_second_writer(self):
        self.run_install(apply=True)
        before = self.snapshot()
        lock = self.target.parent / (".platform-skill-" + self.bundle["id"] + ".lock")
        lock.mkdir()
        with self.assertRaisesRegex(ValueError, "active"):
            self.run_install(package=revise(self.bundle), apply=True)
        self.assertEqual(self.snapshot(), before)

    def test_failed_swap_recovers_old_install(self):
        self.run_install(apply=True)
        before = self.snapshot(self.target)
        rename = Path.rename

        def fail_stage(path, destination):
            if path.parent.name == "skill-staging":
                raise OSError("simulated disk error")
            return rename(path, destination)

        with patch.object(Path, "rename", fail_stage), self.assertRaisesRegex(OSError, "disk error"):
            self.run_install(package=revise(self.bundle), apply=True)
        self.assertEqual(self.snapshot(self.target), before)

    def test_changes_during_check_are_not_overwritten(self):
        self.run_install(apply=True)
        inspect = mod.inspect
        count = 0

        def edit_after_first(*args):
            nonlocal count
            count += 1
            if count == 2:
                (self.target / "SKILL.md").write_text("concurrent user edit")
            return inspect(*args)

        with patch.object(mod, "inspect", edit_after_first), self.assertRaisesRegex(ValueError, "changed"):
            self.run_install(package=revise(self.bundle), apply=True)
        self.assertEqual((self.target / "SKILL.md").read_text(), "concurrent user edit")

    def test_cli_checks_requires_explicit_mode_and_emits_local_receipt(self):
        payload = self.project / "package.json"
        payload.write_text(json.dumps(self.bundle))
        args = ["installer", "--package", str(payload), "--scope", "project", "--project-root", str(self.project)]
        with patch.object(sys, "argv", args + ["--check"]):
            self.assertEqual(mod.main(), 0)
        self.assertFalse(self.target.exists())
        with patch.object(sys, "argv", args + ["--apply"]):
            self.assertEqual(mod.main(), 0)
        self.assertTrue(mod.read_receipt(self.target, self.bundle["id"]))


unittest.main()
