#!/usr/bin/env python3
"""List or delete workflow instances in orchestrator/instances.json."""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import sys
from pathlib import Path
from typing import Any


def town_root_from_env(explicit: str | None) -> Path:
    root = explicit or os.environ.get("GT_ROOT") or os.path.expanduser("~/gt")
    root = Path(root).expanduser().resolve()
    if not (root / "settings" / "config.json").is_file():
        sys.exit(f"FATAL: not a Gas Town root (no settings/config.json): {root}")
    return root


def instances_path(town_root: Path) -> Path:
    return town_root / "orchestrator" / "instances.json"


def load_snapshot(path: Path) -> dict[str, Any]:
    if not path.is_file():
        return {"instances": [], "next_seq": 0}
    try:
        data = json.loads(path.read_text())
    except json.JSONDecodeError as e:
        sys.exit(f"FATAL: decode {path}: {e}")
    if not isinstance(data, dict):
        sys.exit(f"FATAL: expected object in {path}")
    data.setdefault("instances", [])
    data.setdefault("next_seq", 0)
    return data


def lookup_role(town_root: Path, template_id: str, state: str) -> str:
    if not template_id or not state:
        return ""
    tpl = town_root / "orchestrator" / "templates" / f"{template_id}.yaml"
    if not tpl.is_file():
        return ""
    text = tpl.read_text()
    pattern = rf"(?m)^  {re.escape(state)}:\n(?:    .*\n)*?    role: (\S+)"
    m = re.search(pattern, text)
    return m.group(1) if m else ""


def enrich(inst: dict[str, Any], town_root: Path) -> dict[str, Any]:
    vars_ = inst.get("variables") or {}
    rig = vars_.get("rig", "")
    state = inst.get("current_state") or ""
    template_id = inst.get("template_id") or ""
    role = lookup_role(town_root, template_id, state)
    return {
        "id": inst.get("id", ""),
        "template_id": template_id,
        "rig": rig,
        "current_state": state,
        "status": inst.get("status", ""),
        "role": role,
    }


def filter_instances(
    instances: list[dict[str, Any]],
    *,
    rig: str | None,
    template: str | None,
    status: str | None,
    ids: set[str] | None,
) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for inst in instances:
        if not isinstance(inst, dict):
            continue
        iid = (inst.get("id") or "").strip()
        if ids is not None and iid not in ids:
            continue
        vars_ = inst.get("variables") or {}
        if rig and vars_.get("rig") != rig:
            continue
        if template and inst.get("template_id") != template:
            continue
        st = (inst.get("status") or "").lower()
        if status:
            want = status.lower()
            if want == "active":
                if st in ("completed", "failed"):
                    continue
            elif st != want:
                continue
        out.append(inst)
    return out


def duplicate_active_warning(rows: list[dict[str, Any]]) -> str:
    counts: dict[tuple[str, str], int] = {}
    for row in rows:
        st = (row.get("status") or "").lower()
        if st in ("completed", "failed"):
            continue
        key = (row.get("template_id") or "", row.get("rig") or "")
        counts[key] = counts.get(key, 0) + 1
    parts = []
    for (tpl, rig), n in sorted(counts.items()):
        if n <= 1:
            continue
        label = tpl + (f"/{rig}" if rig else "")
        parts.append(f"{n} active for {label}")
    if not parts:
        return ""
    return (
        "multiple orchestrator workflows: "
        + "; ".join(parts)
        + " (fail extras or delete duplicates)"
    )


def orchestrator_running(town_root: Path) -> tuple[bool, int | None]:
    pid_file = town_root / "daemon" / "orchestrator.pid"
    if not pid_file.is_file():
        return False, None
    try:
        pid = int(pid_file.read_text().strip().split()[0])
    except (OSError, ValueError):
        return False, None
    try:
        os.kill(pid, 0)
    except OSError:
        return False, pid
    return True, pid


def format_table(rows: list[dict[str, str]], headers: list[str]) -> str:
    if not rows:
        return ""
    widths = [len(h) for h in headers]
    for row in rows:
        for i, h in enumerate(headers):
            widths[i] = max(widths[i], len(row.get(h, "")))
    lines = []
    hdr = "  ".join(h.ljust(widths[i]) for i, h in enumerate(headers))
    sep = "  ".join("-" * widths[i] for i in range(len(headers)))
    lines.extend([hdr, sep])
    for row in rows:
        lines.append("  ".join(row.get(h, "").ljust(widths[i]) for i, h in enumerate(headers)))
    return "\n".join(lines)


def cmd_list(args: argparse.Namespace) -> int:
    town_root = town_root_from_env(args.town)
    path = instances_path(town_root)
    snap = load_snapshot(path)
    raw = snap.get("instances") or []
    filtered = filter_instances(
        raw,
        rig=args.rig,
        template=args.template,
        status=args.status,
        ids=None,
    )
    rows = [enrich(inst, town_root) for inst in filtered]
    rows.sort(key=lambda r: (
        int(r["id"].split("-", 1)[1]) if re.fullmatch(r"wf-\d+", r["id"]) else 999999,
        r["id"],
    ))

    if args.json:
        print(json.dumps({"instances": rows, "path": str(path), "next_seq": snap.get("next_seq")}, indent=2))
        return 0

    print(f"GT_ROOT={town_root}")
    print(f"instances: {path}")
    print(f"next_seq: {snap.get('next_seq', 0)}")
    if not rows:
        print("\n(no matching workflow instances)")
        return 0

    table_rows = [
        {
            "ID": r["id"],
            "TEMPLATE": r["template_id"],
            "RIG": r["rig"],
            "STATE": r["current_state"],
            "STATUS": r["status"],
            "ROLE": r["role"],
        }
        for r in rows
    ]
    print()
    print(format_table(table_rows, ["ID", "TEMPLATE", "RIG", "STATE", "STATUS", "ROLE"]))
    warn = duplicate_active_warning(rows)
    if warn:
        print(f"\n! {warn}", file=sys.stderr)
    running, pid = orchestrator_running(town_root)
    if running:
        print(f"\n(orchestrator running PID {pid}; live view: gt mayor workflow status)", file=sys.stderr)
    return 0


def write_snapshot(path: Path, snap: dict[str, Any], *, backup: bool) -> None:
    instances = snap.get("instances") or []
    if not instances:
        if path.is_file():
            if backup:
                shutil.copy2(path, path.with_suffix(".json.bak"))
            path.unlink(missing_ok=True)
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.is_file() and backup:
        shutil.copy2(path, path.with_suffix(".json.bak"))
    tmp = path.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(snap, indent=2) + "\n")
    tmp.replace(path)


def cmd_delete(args: argparse.Namespace) -> int:
    town_root = town_root_from_env(args.town)
    path = instances_path(town_root)
    snap = load_snapshot(path)
    all_instances = [i for i in (snap.get("instances") or []) if isinstance(i, dict)]

    ids: set[str] = set(args.ids or [])
    if args.all:
        to_remove = list(all_instances)
    elif args.completed:
        to_remove = [
            i
            for i in all_instances
            if (i.get("status") or "").lower() in ("completed", "failed")
        ]
    elif ids:
        to_remove = [i for i in all_instances if (i.get("id") or "") in ids]
    elif args.rig:
        to_remove = [
            i
            for i in all_instances
            if (i.get("variables") or {}).get("rig") == args.rig
        ]
    else:
        sys.exit("FATAL: pass workflow id(s), or --rig, --completed, or --all")

    to_remove = filter_instances(
        to_remove,
        rig=args.rig if args.rig and not args.all and not args.completed and not ids else None,
        template=args.template,
        status=args.status,
        ids=ids if ids and not args.all and not args.completed else None,
    )

    if not to_remove:
        print("No matching workflow instances to delete.")
        return 0

    remove_ids = {(i.get("id") or "") for i in to_remove}
    kept = [i for i in all_instances if (i.get("id") or "") not in remove_ids]

    print(f"GT_ROOT={town_root}")
    print(f"instances: {path}")
    for inst in sorted(to_remove, key=lambda x: x.get("id", "")):
        vars_ = inst.get("variables") or {}
        print(
            f"  {'[dry-run] ' if args.dry_run else ''}delete {inst.get('id')} "
            f"template={inst.get('template_id')} rig={vars_.get('rig', '')} "
            f"state={inst.get('current_state')} status={inst.get('status')}"
        )

    running, pid = orchestrator_running(town_root)
    if running and not args.dry_run:
        print(
            f"\nWARN: orchestrator is running (PID {pid}). "
            "Stop it first or deleted rows may reappear on save:\n"
            f"  cd {town_root} && gt orchestrator stop",
            file=sys.stderr,
        )
        if not args.force:
            try:
                ans = input("Continue anyway? [y/N] ").strip().lower()
            except EOFError:
                ans = ""
            if ans not in ("y", "yes"):
                print("Aborted.")
                return 1

    if args.dry_run:
        print(f"\n(dry-run: would delete {len(to_remove)}, keep {len(kept)})")
        return 0

    if not args.force:
        try:
            ans = input(f"\nDelete {len(to_remove)} workflow instance(s)? [y/N] ").strip().lower()
        except EOFError:
            ans = ""
        if ans not in ("y", "yes"):
            print("Aborted.")
            return 0

    snap["instances"] = kept
    write_snapshot(path, snap, backup=True)
    print(f"\nDeleted {len(to_remove)} instance(s); {len(kept)} remaining.")
    if not kept:
        print(f"(file removed or empty: {path})")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--town",
        dest="town",
        default=None,
        help="Town root (default: GT_ROOT or ~/gt)",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_list = sub.add_parser("list", help="Table of workflow instances")
    p_list.add_argument("--rig", help="Filter by variables.rig")
    p_list.add_argument("--template", help="Filter by template_id")
    p_list.add_argument(
        "--status",
        help="Filter by status (running, completed, failed, or active=non-terminal)",
    )
    p_list.add_argument("--json", action="store_true", help="JSON output")
    p_list.set_defaults(func=cmd_list)

    p_del = sub.add_parser("delete", help="Remove instances from instances.json")
    p_del.add_argument("ids", nargs="*", help="Workflow ids (e.g. wf-1 wf-2)")
    p_del.add_argument("--rig", help="Delete all instances for this rig")
    p_del.add_argument("--completed", action="store_true", help="Delete completed/failed only")
    p_del.add_argument("--all", action="store_true", help="Delete every instance")
    p_del.add_argument("--template", help="Narrow --rig/--completed/--all by template_id")
    p_del.add_argument("--status", help="Narrow deletes by status")
    p_del.add_argument("--dry-run", action="store_true")
    p_del.add_argument("-f", "--force", action="store_true", help="Skip confirmations")
    p_del.set_defaults(func=cmd_delete)

    args = parser.parse_args()
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
