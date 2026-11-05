#!/usr/bin/env python3
"""Generate release notes from the Git history in a checked-out tag.

The script is intentionally read-only with respect to Git: it verifies that the
requested tag is checked out, discovers the nearest previous release tag, reads
the commit range, and writes a reviewable Markdown file.
"""

from __future__ import annotations

import argparse
import html
import re
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Sequence


TAG_PATTERN = re.compile(
    r"^v(?P<major>\d+)\.(?P<minor>\d+)\.(?P<patch>\d+)"
    r"(?:-(?P<prerelease>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)

GIT_ARGUMENT_PREFIXES = (
    ("rev-parse", "HEAD"),
    ("rev-parse", "--verify"),
    ("tag", "--merged"),
    ("log", "--topo-order"),
)


@dataclass(frozen=True)
class ReleaseVersion:
    major: int
    minor: int
    patch: int
    prerelease: tuple[tuple[int, str | int], ...] | None

    @property
    def sort_key(self) -> tuple[int, int, int, int, tuple[tuple[int, str | int], ...]]:
        return (
            self.major,
            self.minor,
            self.patch,
            0 if self.prerelease is not None else 1,
            self.prerelease or (),
        )


def parse_version_tag(tag: str) -> ReleaseVersion:
    match = TAG_PATTERN.fullmatch(tag)
    if not match:
        raise ValueError(
            "release tag must match vMAJOR.MINOR.PATCH[-PRERELEASE] "
            "(for example: v2.9.17 or v2.9.17-rc.1)"
        )

    prerelease_text = match.group("prerelease")
    prerelease = None
    if prerelease_text is not None:
        prerelease = tuple(
            (1, int(part)) if part.isdigit() else (0, part)
            for part in prerelease_text.split(".")
        )

    return ReleaseVersion(
        major=int(match.group("major")),
        minor=int(match.group("minor")),
        patch=int(match.group("patch")),
        prerelease=prerelease,
    )


def run_git(arguments: Sequence[str], cwd: Path) -> str:
    if len(arguments) < 2 or tuple(arguments[:2]) not in GIT_ARGUMENT_PREFIXES:
        raise RuntimeError(f"unexpected Git command requested: {arguments[0:2]!r}")

    completed = subprocess.run(
        ["git", *arguments],
        cwd=cwd,
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
    )
    return completed.stdout


def markdown_escape_inline(value: str) -> str:
    """Escape HTML and Markdown link/format syntax in commit subjects."""

    value = html.escape(value.strip(), quote=False)
    return re.sub(r"([\\`*_{}\[\]()#+>|~])", r"\\\1", value)


@dataclass(frozen=True)
class Commit:
    sha: str
    subject: str


def parse_commit_log(raw_log: str) -> list[Commit]:
    commits: list[Commit] = []
    for line in raw_log.splitlines():
        if not line:
            continue
        sha, separator, subject = line.partition("\t")
        if not separator or not re.fullmatch(r"[0-9a-f]{40}", sha):
            raise ValueError(f"unexpected Git log entry: {line!r}")
        commits.append(Commit(sha=sha, subject=subject.strip()))
    return commits


def find_previous_release_tag(
    current_tag: str, current_version: ReleaseVersion, repository: Path
) -> str | None:
    raw_tags = run_git(
        ["tag", "--merged", "HEAD", "--format", "%(refname:short)"],
        cwd=repository,
    )
    candidates = []
    for tag in raw_tags.splitlines():
        tag = tag.strip()
        try:
            version = parse_version_tag(tag)
        except ValueError:
            continue
        if version.sort_key < current_version.sort_key:
            candidates.append((version.sort_key, tag))

    if not candidates:
        return None
    return max(candidates, key=lambda item: item[0])[1]


def require_exact_tag_checkout(tag: str, repository: Path) -> None:
    status = run_git(["rev-parse", "--verify", "--quiet", "HEAD"], cwd=repository)
    # `--quiet` still prints the object id on success; an empty output means HEAD
    # is unborn, which is not a valid release checkout.
    head_commit = status.strip()
    if not head_commit:
        raise ValueError("HEAD has no commit; check out the release tag first")

    tag_commit = run_git(
        ["rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}"],
        cwd=repository,
    ).strip()
    if head_commit != tag_commit:
        raise ValueError(
            f"tag {tag} does not match the checked-out commit: "
            f"HEAD={head_commit}, tag={tag_commit}"
        )


def generate_release_notes(tag: str, output: Path, repository: Path) -> None:
    current_version = parse_version_tag(tag)
    require_exact_tag_checkout(tag, repository)

    previous_tag = find_previous_release_tag(tag, current_version, repository)
    if previous_tag is None:
        previous_commit = None
        commit_range = "beginning of history"
        raw_log = run_git(
            [
                "log",
                "--topo-order",
                "--reverse",
                "--format=%H%x09%s",
                "HEAD",
            ],
            cwd=repository,
        )
    else:
        previous_commit = run_git(
            ["rev-parse", "--verify", f"refs/tags/{previous_tag}^{{commit}}"],
            cwd=repository,
        ).strip()
        commit_range = f"{previous_tag}...{tag}"
        raw_log = run_git(
            [
                "log",
                "--topo-order",
                "--reverse",
                "--format=%H%x09%s",
                f"{previous_commit}..HEAD",
            ],
            cwd=repository,
        )

    commits = parse_commit_log(raw_log)
    head_commit = run_git(["rev-parse", "HEAD"], cwd=repository).strip()

    lines = [
        f"# Release notes for {markdown_escape_inline(tag)}",
        "",
        "## Provenance",
        "",
        f"- Tag: `{tag}`",
        f"- Current commit: `{head_commit}`",
        f"- Previous tag: `{previous_tag}`" if previous_tag else "- Previous tag: none",
        f"- Previous commit: `{previous_commit}`"
        if previous_commit
        else "- Previous commit: none",
        f"- Commit range: `{commit_range}`",
        f"- Commit count: {len(commits)}",
        "",
        "## Commits",
        "",
    ]
    if commits:
        for commit in commits:
            lines.append(f"- `{commit.sha}` {markdown_escape_inline(commit.subject)}")
    else:
        lines.append("No commits found in the selected range.")

    output.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Generate Markdown release notes from an exact tag checkout."
    )
    parser.add_argument("--tag", required=True, help="release tag to describe")
    parser.add_argument("--output", required=True, type=Path, help="Markdown output file")
    args = parser.parse_args(argv)

    try:
        generate_release_notes(args.tag, args.output, Path.cwd())
    except (OSError, RuntimeError, ValueError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"error: {error}\n")
    print(f"Generated release notes: {args.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
