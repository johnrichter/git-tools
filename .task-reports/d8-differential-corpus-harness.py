#!/usr/bin/env python3
"""Differential-corpus harness: check_privacy.py vs `git-tools scan privacy`.

Review evidence for `.task-reports/d8-privacy-scan-migration-quality-review.md`.
Committed so the review's parity result is reproducible instead of narrated --
D8's original parity claim rested on an uncommitted harness and was wrong.

Stdlib only, no venv needed.

    python3 .task-reports/d8-differential-corpus-harness.py <scratch-dir> [repo ...]

For each repo it snapshots `git ls-files` into <scratch-dir>, indexes the copy
(`git add`, never `git commit`, so no branch is ever touched), then runs the
SNAPSHOT'S OWN copy of check_privacy.py against it -- the script excludes its
own path (check_privacy.py:247-248), so invoking any other copy changes what
gets scanned -- head to head with the git-tools binary at the same tier and
`--strict`. Findings are compared as a set of (path, normalized-category)
pairs, since the two sides label the same rule differently and Python reports
one finding per (file, pattern) where Go reports one per match.

Two harness hazards this handles, both of which produced false parity results
before they were understood:

  1. `privacy_warnings_found` is a separate counter from
     `privacy_violations_found`. Under `--strict` a warning is a real failure.
     Counting only violations reports "0 findings" on a tree that exits 30.
  2. `githooks.EmitHookResult` caps `errors[]` at 50 diagnostics
     (envelope.go:14) and says so via a `caveats.githooks.findings_truncated`
     caveat. A set built from `errors[]` that ignores the caveat undercounts.
     On truncation this harness re-runs per file.

<scratch-dir> receives verbatim copies of every scanned repo's tracked content,
including private repos. Delete it when done.
"""
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

PSA = Path(os.environ.get("PSA_ROOT", "/home/bits/Development/workspaces/psa-platform"))
# Point GIT_TOOLS_BIN at the binary under test (`go build -o <path> ./cmd/git-tools`).
BIN = Path(os.environ.get("GIT_TOOLS_BIN", "./git-tools"))

# (repo, tier check_privacy.py is invoked with, git-tools tier for the same posture)
REPOS = [
    ("marketplace", "public", "public"),
    ("workspace", "personal", "private"),
    ("knowledge-public-datadog", "public", "public"),
    ("knowledge-private-datadog", "datadog", "confidential"),
    ("knowledge-private-personal", "personal", "private"),
    ("marketplace-datadog", "datadog", "confidential"),
    ("ai-shared-lib-datadog", "datadog", "confidential"),
]

# One token per rule class, so the two sides' wording differences (an
# already-shipped Track B convention) do not read as behavioral divergence.
CATS = [
    (re.compile(r"forbidden frontmatter marker", re.I), "forbidden_marker"),
    (re.compile(r"declares .*tag but not", re.I), "not_public_pair"),
    (re.compile(r"private key", re.I), "secret:private_key"),
    (re.compile(r"aws[ _-]access[ _-]key", re.I), "secret:aws_access_key_id"),
    (re.compile(r"slack token", re.I), "secret:slack_token"),
    (re.compile(r"github token", re.I), "secret:github_token"),
    (re.compile(r"internal hostname", re.I), "iid:internal_hostname"),
    (re.compile(r"private/loopback", re.I), "iid:private_network_url"),
    (re.compile(r"issue-tracker", re.I), "iid:tracker_link"),
    (re.compile(r"employee email", re.I), "iid:employee_email"),
]

GIT_ENV = dict(os.environ, GIT_CONFIG_GLOBAL="/dev/null", GIT_CONFIG_SYSTEM="/dev/null")


def cat(text):
    for rx, name in CATS:
        if rx.search(text):
            return name
    return "UNCLASSIFIED:" + text.strip()


def index(tree):
    subprocess.run(["git", "-C", str(tree), "init", "-q", "-b", "snap"],
                   check=True, env=GIT_ENV)
    subprocess.run(["git", "-C", str(tree), "add", "-A", "-f"], check=True, env=GIT_ENV)


def tracked(tree):
    out = subprocess.run(["git", "-C", str(tree), "ls-files", "-z"],
                         capture_output=True, check=True).stdout
    return [r.decode("utf-8", "surrogateescape") for r in out.split(b"\0") if r]


def snapshot(repo, scratch):
    src, dst = PSA / repo, scratch / repo
    if dst.exists():
        shutil.rmtree(dst)
    dst.mkdir(parents=True)
    copied = 0
    for rel in tracked(src):
        s = src / rel
        if not s.is_file():
            continue
        d = dst / rel
        d.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(s, d)
        copied += 1
    index(dst)
    return dst, copied


def run_python(snap, tier):
    p = subprocess.run([sys.executable, str(snap / "scripts/check_privacy.py"),
                        "--tier", tier, "--root", str(snap), "--strict"],
                       capture_output=True, text=True)
    findings = set()
    for line in (p.stdout + p.stderr).splitlines():
        if line.startswith("  - "):
            path, _, rest = line[4:].partition(": ")
            findings.add((path.strip(), cat(rest)))
    return p.returncode, findings


def go_once(target, tier):
    """One git-tools run -> (rc, findings, truncated)."""
    p = subprocess.run([str(BIN), "scan", "privacy", "--repo", str(target),
                        "--privacy-tier", tier, "--strict"],
                       capture_output=True, text=True)
    findings = set()
    try:
        env = json.loads(p.stdout)
    except json.JSONDecodeError:
        return p.returncode, findings, False
    diagnostics = (env.get("errors") or []) + (env.get("caveats") or [])
    truncated = any(d.get("code") == "caveats.githooks.findings_truncated"
                    for d in diagnostics)
    for d in diagnostics:
        ctx = d.get("context") or {}
        if "path" in ctx:
            findings.add((ctx["path"], cat(d.get("message", "") + " " + ctx.get("rule", ""))))
    return p.returncode, findings, truncated


def run_go(snap, tier, scratch):
    rc, findings, truncated = go_once(snap, tier)
    if not truncated:
        return rc, findings, []
    split = scratch / "_split"
    findings, still = set(), []
    for rel in tracked(snap):
        src = snap / rel
        if not src.is_file():
            continue
        if split.exists():
            shutil.rmtree(split)
        d = split / rel
        d.parent.mkdir(parents=True)
        os.link(src, d)
        index(split)
        _, f, t = go_once(split, tier)
        findings |= f
        if t:
            still.append(rel)
    if split.exists():
        shutil.rmtree(split)
    return rc, findings, still


def main(argv):
    if not argv:
        print(__doc__)
        return 2
    scratch, only = Path(argv[0]), set(argv[1:])
    scratch.mkdir(parents=True, exist_ok=True)
    rows, mismatched = [], []
    for repo, pytier, gotier in REPOS:
        if only and repo not in only:
            continue
        if not (PSA / repo).is_dir():
            print(f"SKIP {repo}: not present")
            continue
        snap, n = snapshot(repo, scratch)
        pyrc, pyf = run_python(snap, pytier)
        gorc, gof, still = run_go(snap, gotier, scratch)
        only_py, only_go = sorted(pyf - gof), sorted(gof - pyf)
        ok = not (only_py or only_go)
        rows.append((repo, f"{pytier} -> {gotier}", n, len(pyf), len(gof), "yes" if ok else "**no**"))
        if not ok:
            mismatched.append(repo)
        print(f"{repo:<28} files={n:<5} py={len(pyf):<3}(rc={pyrc}) "
              f"go={len(gof):<3}(rc={gorc}) {'MATCH' if ok else 'MISMATCH'}")
        for p, c in only_py:
            print(f"   PY-ONLY  {p}  {c}")
        for p, c in only_go:
            print(f"   GO-ONLY  {p}  {c}")
        for rel in still:
            print(f"   NOTE: per-file run still truncated for {rel}; "
                  "its category set may be incomplete")

    print("\n| Repo | Tier (py -> go) | Tracked files | Python | Go | Match |")
    print("|------|-----------------|---------------|--------|----|-------|")
    tf = tp = tg = 0
    for repo, tier, n, np_, ng, m in rows:
        print(f"| {repo} | {tier} | {n} | {np_} | {ng} | {m} |")
        tf, tp, tg = tf + n, tp + np_, tg + ng
    print(f"| **Total** | | **{tf}** | **{tp}** | **{tg}** | "
          f"**{len(rows) - len(mismatched)} of {len(rows)}** |")
    print(f"\nRemove {scratch} -- it holds verbatim copies of every scanned repo.")
    return 1 if mismatched else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
