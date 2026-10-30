"""Read saved worker replies and their actual persistence evidence; no provider calls."""
import argparse
import collections
import datetime
import json
import sqlite3
from pathlib import Path


def utc(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def read_json(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def local_file(folder, name):
    if not isinstance(name, str) or name in ("", ".", "..") or "/" in name or "\\" in name:
        raise ValueError("configured filenames must be flat")
    return folder / name


def readonly(path):
    db = sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True)
    db.row_factory = sqlite3.Row
    db.execute("BEGIN")
    return db


def decoded(row):
    result = dict(row)
    result["fields"] = json.loads(result["details"])
    return result


def actor_key(row, role=None):
    data = row["fields"]
    owner = data.get("worker", data.get("worker_id"))
    return (owner, data.get("phase"), role or row["scope"], row.get("drone_id"))


def reply_format(text):
    lines = [line.strip() for line in text.splitlines() if line.strip()]
    if not lines:
        return "empty"
    if "# ACTION" in lines:
        return "legacy_h1"
    # This is a display classification, never a second acceptance parser.
    return "non_h1"


def purpose(row, request):
    tools = request.get("tools", []) if isinstance(request, dict) else []
    if any(isinstance(tool, dict) and str(tool.get("type", "")).startswith("web_search") for tool in tools):
        return "research"
    phase = row["fields"].get("phase")
    if row["scope"] == "queen" and phase == "queen_monitor":
        return "queen_monitor"
    if row["scope"] == "drone" and phase in ("contribute", "expand"):
        return "drone_action"
    return "other"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("run_folder", type=Path)
    parser.add_argument("output_json", type=Path)
    parser.add_argument("--boundary", type=Path)
    parser.add_argument("--since", help="Override boundary started_utc with an exact UTC timestamp")
    args = parser.parse_args()
    if args.output_json.exists():
        raise FileExistsError("Refusing to overwrite " + str(args.output_json))
    run = args.run_folder.resolve()
    boundary_path = args.boundary or run / "simple-format-boundary.json"
    boundary = read_json(boundary_path)
    since_text = args.since or boundary["started_utc"]
    since = utc(since_text)
    floor = since.strftime("%Y-%m-%dT%H:%M:%S")
    config = read_json(run / "config.json")
    debug_path = local_file(run, config["files"]["debug"])
    state_path = local_file(run, config["files"]["state"])
    debug = readonly(debug_path)
    try:
        log_boundary = dict(debug.execute("SELECT id,ts FROM hive_log ORDER BY id DESC LIMIT 1").fetchone())
        events = [decoded(row) for row in debug.execute(
            "SELECT * FROM hive_log WHERE ts>=? AND (event LIKE 'llm.%' OR event IN "
            "('inquiry_reply','inquiry_action_rejected','worker_action_saved','worker_queen_reply',"
            "'worker_queen_decision','worker_pool_finished') OR outcome IN('error','format_error','refusal')) ORDER BY id", (floor,))
            if utc(row["ts"]) >= since]
    finally:
        debug.close()
    state = readonly(state_path)
    try:
        work = {row["id"]: dict(row) for row in state.execute("SELECT * FROM hive_inquiry_work WHERE created>=?", (floor,)) if utc(row["created"]) >= since}
        guidance = [dict(row) for row in state.execute("SELECT * FROM hive_worker_guidance WHERE created>=? ORDER BY id", (floor,)) if utc(row["created"]) >= since]
        requests = [dict(row) for row in state.execute("SELECT id,status,finish_reason FROM hive_requests ORDER BY id")]
        claims = [dict(row) for row in state.execute("SELECT drone_id,request_id,owner,phase FROM hive_worker_queue WHERE owner<>''")]
        queen_claims = [dict(row) for row in state.execute("SELECT request_id,owner FROM hive_worker_queen WHERE owner<>''")]
        max_work = state.execute("SELECT id,created FROM hive_inquiry_work ORDER BY id DESC LIMIT 1").fetchone()
        max_work = dict(max_work) if max_work else None
    finally:
        state.close()

    calls, active, latest, pending = [], {}, {}, {}
    rejections, unmatched, other_errors = [], [], []
    for row in events:
        event, fields = row["event"], row["fields"]
        key = actor_key(row)
        if event == "llm.start":
            request = fields.get("request")
            try:
                parsed = json.loads(request) if isinstance(request, str) else request
            except (TypeError, ValueError):
                parsed = None
            call = {"source_db": str(debug_path), "log_table": "hive_log", "start_id": row["id"],
                    "started_utc": row["ts"], "scope": row["scope"], "drone_id": row["drone_id"],
                    "phase": fields.get("phase"), "model_requested": row["model"], "purpose": purpose(row, parsed),
                    "start": row, "responses": [], "finish": None, "reply": None,
                    "raw_format": None, "acceptance": None, "rejections": []}
            calls.append(call)
            active[key] = call
        elif event in ("llm.response", "llm.finish"):
            call = active.get(key)
            if call is None:
                unmatched.append({"id": row["id"], "event": event, "reason": "no new call start inside boundary"})
                continue
            if event == "llm.response":
                call["responses"].append(row)
            else:
                call["finish"] = row
                latest[key] = call
                del active[key]
        elif event in ("inquiry_reply", "worker_queen_reply"):
            role = "queen" if event == "worker_queen_reply" else "drone"
            key = actor_key(row, role)
            call = latest.get(key)
            if call is None:
                unmatched.append({"id": row["id"], "event": event, "reason": "no completed new call"})
                continue
            call["reply"] = row
            call["raw_format"] = reply_format(fields.get("body", ""))
            pending[key] = call
        elif event == "inquiry_action_rejected":
            rejections.append(row)
            call = pending.get(actor_key(row, "drone"))
            if call is not None:
                call["rejections"].append(row)
        elif event in ("worker_action_saved", "worker_queen_decision"):
            role = "queen" if event == "worker_queen_decision" else "drone"
            key = actor_key(row, role)
            call = pending.pop(key, None)
            if call is None:
                unmatched.append({"id": row["id"], "event": event, "reason": "no pending new action reply"})
                continue
            action = fields.get("action", {})
            evidence = {"committed_log": row, "work": None, "guidance": [], "gaps": []}
            if role == "drone":
                work_id = fields.get("work_id", 0)
                if work_id:
                    evidence["work"] = work.get(work_id)
                    if evidence["work"] is None:
                        evidence["gaps"].append("post-commit work_id missing from new operational snapshot")
                elif action.get("kind") != "PASS":
                    evidence["gaps"].append("non-PASS committed action has no work_id")
            elif action.get("kind") == "GUIDE":
                evidence["guidance"] = [entry for entry in guidance
                    if entry["request_id"] == row["request_id"] and entry["inquiry_id"] == fields.get("inquiry_id")
                    and entry["body"] == action.get("body") and entry["relevance"] == action.get("relevance")
                    and entry["inquiry_value"] == action.get("inquiry_value")
                    and utc(call["reply"]["ts"]) <= utc(entry["created"]) <= utc(row["ts"])]
                if not evidence["guidance"]:
                    evidence["gaps"].append("post-commit GUIDE has no matching operational guidance row")
            call["acceptance"] = evidence
        elif row["outcome"] in ("error", "format_error", "refusal"):
            other_errors.append(row)

    model_counts, http_counts, outcome_counts, formats, accepted = (collections.Counter() for _ in range(5))
    for call in calls:
        finish = call["finish"]
        model_counts[(finish or call["start"]).get("model") or "unknown"] += 1
        outcome_counts[finish["outcome"] if finish else "unfinished"] += 1
        for response in call["responses"]:
            http_counts[str(response["fields"].get("status_code", "unknown"))] += 1
        if call["reply"]:
            formats[call["purpose"] + ":" + call["raw_format"]] += 1
        if call["acceptance"] and not call["acceptance"]["gaps"]:
            accepted[call["purpose"] + ":" + call["raw_format"]] += 1
    latest_results = sorted(run.glob("worker-result-*.json"))
    latest_result = {"path": str(latest_results[-1]), "value": read_json(latest_results[-1])} if latest_results else None
    summary = {"new_calls_started": len(calls), "new_calls_finished": sum(call["finish"] is not None for call in calls),
               "actual_or_requested_models": dict(model_counts), "http_response_events": dict(http_counts),
               "provider_outcomes": dict(outcome_counts), "reply_formats": dict(formats),
               "accepted_with_saved_evidence": dict(accepted), "local_rejection_events": len(rejections),
               "other_error_events": len(other_errors), "unmatched_event_count": len(unmatched)}
    result = {"generated_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(), "run_folder": str(run),
              "boundary_path": str(boundary_path), "boundary": boundary, "effective_since_utc": since_text,
              "debug_snapshot_boundary": log_boundary, "state_max_work": max_work, "requests": requests,
              "owned_drone_claims": claims, "owned_queen_claims": queen_claims, "latest_result": latest_result,
              "method": "SQLite mode=ro snapshots; calls require llm.start after boundary. Raw non-H1 classification is descriptive, not a second parser. Acceptance requires post-transaction action/decision log, and operational work or GUIDE row where applicable. Provider events and local rejections are separate counts.",
              "limits": "A brief saved observation does not establish general reliability or intellectual quality. Research and other calls are classified separately. No native billing dollars are inferred.",
              "summary": summary, "calls": calls, "local_rejections": rejections, "other_errors": other_errors,
              "unmatched_events": unmatched, "new_work": list(work.values()), "new_guidance": guidance}
    with args.output_json.open("x", encoding="utf-8", newline="\n") as handle:
        json.dump(result, handle, ensure_ascii=False, indent=2)
        handle.write("\n")
    print(json.dumps({"output": str(args.output_json.resolve()), **summary}, ensure_ascii=False))


if __name__ == "__main__":
    main()
