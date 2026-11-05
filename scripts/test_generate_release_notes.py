from __future__ import annotations

import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("generate-release-notes.py")
spec = importlib.util.spec_from_file_location("generate_release_notes", SCRIPT_PATH)
release_notes = importlib.util.module_from_spec(spec)
assert spec.loader is not None
sys.modules[spec.name] = release_notes
spec.loader.exec_module(release_notes)


def run_git(repository: Path, *arguments: str) -> str:
    return subprocess.run(
        ["git", *arguments],
        cwd=repository,
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
    ).stdout


def create_repository(temp_directory: Path) -> Path:
    repository = temp_directory / "repository"
    repository.mkdir()
    run_git(repository, "init", "--initial-branch=main")
    return repository


def commit(repository: Path, message: str) -> None:
    path = repository / "file.txt"
    path.write_text(message, encoding="utf-8")
    run_git(repository, "add", str(path))
    run_git(repository, "commit", "--allow-empty", "-m", message)


class GenerateReleaseNotesTests(unittest.TestCase):
    def setUp(self) -> None:
        self._temporary_context = tempfile.TemporaryDirectory()
        self.temp_directory = Path(self._temporary_context.name)
        self.repository = create_repository(self.temp_directory)

    def tearDown(self) -> None:
        self._temporary_context.cleanup()

    def test_rejects_network_and_mutating_git_commands(self) -> None:
        for arguments in (
            ("fetch", "--all"),
            ("pull",),
            ("push", "origin", "main"),
            ("add", "."),
            ("commit", "--allow-empty"),
        ):
            with self.subTest(arguments=arguments):
                with self.assertRaises(RuntimeError):
                    release_notes.run_git(arguments, self.temp_directory)

    def generate_notes(self, tag: str) -> Path:
        output = self.temp_directory / "release-notes.md"
        completed = subprocess.run(
            [
                sys.executable,
                str(SCRIPT_PATH),
                "--tag",
                tag,
                "--output",
                str(output),
            ],
            cwd=self.repository,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(completed.returncode, 0, completed.stderr)
        return output

    def test_uses_nearest_previous_release_tag_for_commit_range(self) -> None:
        commit(self.repository, "initial release")
        run_git(self.repository, "tag", "v1.0.0")
        commit(self.repository, "intermediate fix")
        run_git(self.repository, "tag", "v1.1.0")
        commit(self.repository, "new release feature")
        run_git(self.repository, "tag", "v2.0.0")

        output = self.generate_notes("v2.0.0")
        markdown = output.read_text(encoding="utf-8")
        current_sha = run_git(self.repository, "rev-parse", "HEAD").strip()
        previous_sha = run_git(
            self.repository, "rev-parse", "refs/tags/v1.1.0^{commit}"
        ).strip()

        self.assertIn("# Release notes for v2.0.0", markdown)
        self.assertIn("- Tag: `v2.0.0`", markdown)
        self.assertIn(f"- Current commit: `{current_sha}`", markdown)
        self.assertIn("- Previous tag: `v1.1.0`", markdown)
        self.assertIn(f"- Previous commit: `{previous_sha}`", markdown)
        self.assertIn("- Commit range: `v1.1.0...v2.0.0`", markdown)
        self.assertIn("new release feature", markdown)
        self.assertNotIn("initial release", markdown)

    def test_uses_beginning_of_history_without_a_previous_tag(self) -> None:
        commit(self.repository, "first release feature")
        run_git(self.repository, "tag", "v1.0.0")

        output = self.generate_notes("v1.0.0")
        markdown = output.read_text(encoding="utf-8")

        self.assertIn("- Previous tag: none", markdown)
        self.assertIn("- Previous commit: none", markdown)
        self.assertIn("- Commit range: `beginning of history`", markdown)
        self.assertIn("first release feature", markdown)

    def test_rejects_invalid_release_tag(self) -> None:
        commit(self.repository, "initial release")
        run_git(self.repository, "tag", "v1.0.0")

        output = self.temp_directory / "release-notes.md"
        completed = subprocess.run(
            [
                sys.executable,
                str(SCRIPT_PATH),
                "--tag",
                "v1.0.0-..",
                "--output",
                str(output),
            ],
            cwd=self.repository,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("release tag must match", completed.stderr)
        self.assertFalse(output.exists())

    def test_rejects_tag_that_is_not_checked_out(self) -> None:
        commit(self.repository, "first release")
        run_git(self.repository, "tag", "v1.0.0")
        commit(self.repository, "second release")

        output = self.temp_directory / "release-notes.md"
        completed = subprocess.run(
            [
                sys.executable,
                str(SCRIPT_PATH),
                "--tag",
                "v1.0.0",
                "--output",
                str(output),
            ],
            cwd=self.repository,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("does not match the checked-out commit", completed.stderr)
        self.assertFalse(output.exists())

    def test_escapes_markdown_in_commit_subject_and_includes_provenance(self) -> None:
        commit(self.repository, "first release")
        run_git(self.repository, "tag", "v1.0.0")
        commit(self.repository, "[bad](javascript:alert(1)) # heading")
        run_git(self.repository, "tag", "v2.0.0")

        output = self.generate_notes("v2.0.0")
        markdown = output.read_text(encoding="utf-8")

        self.assertIn("- Previous commit: `", markdown)
        self.assertIn("- Current commit: `", markdown)
        self.assertIn("- Commit count: 1", markdown)
        self.assertNotIn("[bad](javascript:alert(1)) # heading", markdown)


if __name__ == "__main__":
    unittest.main()
