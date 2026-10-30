"""Read saved inquiry evidence and diagnostics. No writes to sources or model calls."""
import argparse
import collections
import contextlib
import datetime
import json
import sqlite3
from pathlib import Path


TABLES = (
    "hive_requests", "hive_runs", "hive_drones", "hive_inquiries",
    "hive_inquiry_members", "hive_inquiry_work",
)
COUNTERS = ("input", "cached", "cache_write", "output", "reasoning", "total")


def decode(raw):
    if raw is None or isinstance(raw, (dict, list)):
        return raw, None
    try:
        def invalid(value):
            raise ValueError("non-JSON numeric value: " + value)
        return json.loads(raw, parse_constant=invalid), None
    except (ValueError, TypeError) as exc:
        return None, str(exc)


def obj(value):
    return value if isinstance(value, dict) else {}


def integer(value):
    return isinstance(value, int) and not isinstance(value, bool) and value >= 0


@contextlib.contextmanager
def readonly(path):
    db = sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True)
    db.row_factory = sqlite3.Row
    try:
        db.execute("BEGIN")
        yield db
    finally:
        db.close()


def rows(db, query):
    return [dict(row) for row in db.execute(query)]


def local_file(run, name):
    if not isinstance(name, str) or name in ("", ".", "..") or Path(name).name != name or "/" in name or "\\" in name:
        raise ValueError("configured runtime filenames must be flat")
    return run / name


def load_state(path):
    result = {"source_db": str(path), "available": path.is_file(), "tables": {}, "missing_tables": []}
    if not result["available"]:
        result["missing_tables"] = list(TABLES)
        return result
    with readonly(path) as db:
        names = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        for table in TABLES:
            if table not in names:
                result["missing_tables"].append(table)
                continue
            order = "inquiry_id,drone_id" if table == "hive_inquiry_members" else "id"
            result["tables"][table] = rows(db, f"SELECT * FROM {table} ORDER BY {order}")
    return result


def token_usage(call):
    logged = obj(call.get("usage"))
    native = obj(obj(call.get("response")).get("usage"))
    logged_input = obj(logged.get("input_tokens_details"))
    native_input = obj(native.get("prompt_tokens_details"))
    logged_output = obj(logged.get("output_tokens_details"))
    native_output = obj(native.get("completion_tokens_details"))
    sources = {
        "input": (logged.get("input_tokens"), native.get("prompt_tokens")),
        "cached": (logged_input.get("cached_tokens"), native_input.get("cached_tokens")),
        "cache_write": (logged_input.get("cache_write_tokens"), native_input.get("cache_write_tokens")),
        "output": (logged.get("output_tokens"), native.get("completion_tokens")),
        "reasoning": (logged_output.get("reasoning_tokens"), native_output.get("reasoning_tokens")),
        "total": (logged.get("total_tokens"), native.get("total_tokens")),
    }
    counters, checks = {}, []
    for name, (finish, raw) in sources.items():
        counters[name] = finish if integer(finish) else raw if integer(raw) else None
        if integer(finish) and integer(raw) and finish != raw:
            checks.append({"counter": name, "logged": finish, "native": raw, "kind": "usage_mismatch"})
    if all(integer(counters[name]) for name in ("input", "output", "total")):
        if counters["input"] + counters["output"] != counters["total"]:
            checks.append({"kind": "total_differs_from_input_plus_output"})
    for part, whole in (("cached", "input"), ("reasoning", "output")):
        if integer(counters[part]) and integer(counters[whole]) and counters[part] > counters[whole]:
            checks.append({"kind": "subset_exceeds_total", "part": part, "whole": whole})
    call["native_usage"] = native or None
    call["token_counters"], call["usage_checks"] = counters, checks


def same_actor(event, call):
    return call is not None and all(
        event.get(key) is not None and event.get(key) == call.get(key)
        for key in ("run_id", "request_id", "drone_id", "phase")
    )


def load_debug(path):
    result = {"source_db": str(path), "available": path.is_file(), "calls": [], "events": [],
              "failure_events": [], "association_anomalies": [], "event_counts": [], "sql_counts": []}
    if not result["available"]:
        return result
    active, latest = None, None
    with readonly(path) as db:
        if not db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='hive_log'").fetchone():
            result["available"] = False
            result["association_anomalies"].append({"kind": "missing_hive_log"})
            return result
        result["event_counts"] = rows(db, "SELECT event,outcome,count(*) AS count FROM hive_log GROUP BY event,outcome ORDER BY event,outcome")
        result["sql_counts"] = rows(db, "SELECT event,sql_op,table_name,outcome,count(*) AS count FROM hive_log WHERE sql_op IS NOT NULL GROUP BY event,sql_op,table_name,outcome ORDER BY event,sql_op,table_name,outcome")
        for row in rows(db, "SELECT * FROM hive_log WHERE event NOT IN ('sql_start','sql_done') ORDER BY id"):
            details, error = decode(row["details"])
            data = obj(details)
            event = dict(row, source_db=str(path), log_table="hive_log", details=data,
                         details_raw=row["details"], details_parse_error=error, phase=data.get("phase"))
            kind = row["event"]
            identity = {key: event.get(key) for key in ("run_id", "request_id", "drone_id", "phase", "scope")}
            event["associated_call_id"] = None
            if error:
                result["association_anomalies"].append({"log_id": row["id"], "kind": "invalid_log_details", "error": error})
            if kind == "llm.start":
                if active is not None:
                    result["association_anomalies"].append({"log_id": row["id"], "kind": "new_start_before_finish", "previous_call": active["call_id"]})
                request, parse_error = decode(data.get("request"))
                active = dict(identity, call_id=f"{path}:hive_log:{row['id']}", source_db=str(path), log_table="hive_log",
                              start_log_id=row["id"], started=row["ts"], requested_model=data.get("model"), model=data.get("model"),
                              request=request, request_raw=data.get("request"), request_parse_error=parse_error,
                              responses=[], outcome="incomplete", action_events=[])
                result["calls"].append(active)
                latest = None
                event["associated_call_id"] = active["call_id"]
            elif kind == "llm.response":
                body, parse_error = decode(data.get("body"))
                response = {"log_id": row["id"], "ts": row["ts"], "http_status": data.get("status_code"),
                            "body": body, "body_raw": data.get("body"), "parse_error": parse_error}
                if active is None:
                    result["association_anomalies"].append(dict(response, kind="response_without_start"))
                else:
                    active["responses"].append(response)
                    active.update(response=body, response_raw=data.get("body"), http_status=data.get("status_code"))
                    event["associated_call_id"] = active["call_id"]
            elif kind == "llm.finish":
                if active is None:
                    result["association_anomalies"].append({"log_id": row["id"], "kind": "finish_without_start", "details": data})
                else:
                    active.update(finish_log_id=row["id"], finished=row["ts"], outcome=data.get("outcome"),
                                  duration_ms=data.get("duration_ms"), usage=data.get("usage"), reply=data.get("reply"),
                                  refusal=data.get("refusal"), error=data.get("error"), provider_error_kind=data.get("kind"),
                                  model=obj(active.get("response")).get("model") or data.get("model") or active["model"])
                    if active.get("http_status") is None and data.get("status_code"):
                        active["http_status"] = data["status_code"]
                    event["associated_call_id"] = active["call_id"]
                    latest, active = active, None
            elif kind in ("inquiry_reply", "inquiry_action", "inquiry_action_rejected", "inquiry_pass") and same_actor(event, latest):
                event["associated_call_id"] = latest["call_id"]
                latest["action_events"].append({"log_id": row["id"], "event": kind, "details": data})
            category = None
            if kind == "llm.finish" and (data.get("error") or data.get("refusal") or data.get("outcome") == "refusal"):
                category = "provider"
            elif kind == "llm.response" and isinstance(data.get("status_code"), int) and not 200 <= data["status_code"] < 300:
                category = "provider_http"
            elif kind == "inquiry_action_rejected":
                category = "action_parse" if data.get("outcome") == "format_error" else "action_application"
            elif data.get("error"):
                category = "runtime"
            if category:
                result["failure_events"].append(dict(event, category=category))
            if kind.startswith("llm.") or kind.startswith("inquiry_") or category:
                result["events"].append(event)
    for call in result["calls"]:
        token_usage(call)
    return result


def totals(calls):
    counters = {}
    for name in COUNTERS:
        known = [c["token_counters"][name] for c in calls if integer(c["token_counters"][name])]
        counters[name] = {"known_sum": sum(known) if known or not calls else None, "unknown_calls": len(calls) - len(known)}
    return {"calls": len(calls), "outcomes": dict(collections.Counter(str(c.get("outcome")) for c in calls)),
            "tokens": counters, "cache_hit_calls": sum(integer(c["token_counters"]["cached"]) and c["token_counters"]["cached"] > 0 for c in calls),
            "cache_unknown_calls": counters["cached"]["unknown_calls"],
            "known_provider_duration_ms": sum(c["duration_ms"] for c in calls if integer(c.get("duration_ms"))),
            "unknown_duration_calls": sum(not integer(c.get("duration_ms")) for c in calls)}


def evidence_summary(state):
    tables = state["tables"]
    work, members = tables.get("hive_inquiry_work", []), tables.get("hive_inquiry_members", [])
    teams, overlapping = [], collections.defaultdict(list)
    for inquiry in tables.get("hive_inquiries", []):
        roster = [m for m in members if m["inquiry_id"] == inquiry["id"]]
        committed = {m["drone_id"] for m in roster}
        contributions = [w for w in work if w["inquiry_id"] == inquiry["id"]]
        contributors = sorted({w["drone_id"] for w in contributions if w["drone_id"] in committed and w["kind"] in ("PROPOSE", "JOIN", "CONTRIBUTE")})
        teams.append({"inquiry_id": inquiry["id"], "request_id": inquiry["request_id"], "question": inquiry["question"],
                      "proposed_by": inquiry["proposed_by"], "members": sorted(committed), "contributors": contributors,
                      "observer_qualified": len(committed) > 1 and len(contributors) > 1,
                      "work_ids": [w["id"] for w in contributions]})
        for member in committed:
            overlapping[member].append(inquiry["id"])
    chronology = []
    for table in ("hive_inquiries", "hive_inquiry_members", "hive_inquiry_work"):
        chronology.extend({"source_table": table, "record": row} for row in tables.get(table, []))
    chronology.sort(key=lambda item: (item["record"]["created"], TABLES.index(item["source_table"]), item["record"].get("id", 0)))
    return {"teams": teams, "overlapping_memberships": [{"drone_id": key, "inquiry_ids": value} for key, value in sorted(overlapping.items()) if len(value) > 1],
            "connections": [row for row in work if row["kind"] == "CONNECT"], "chronology": chronology}


def saved_json(run, name):
    path = run / name
    if not path.is_file():
        return None
    raw = path.read_text(encoding="utf-8-sig")
    parsed, error = decode(raw)
    return {"path": str(path), "value": parsed, "raw": raw, "parse_error": error}


def block(text):
    text = "" if text is None else str(text)
    fence = "````"
    while fence in text:
        fence += "`"
    return f"{fence}\n{text}\n{fence}"


def markdown(report):
    state, debug, summary = report["state"], report["debug"], report["summary"]
    lines = ["# Inquiry observation", "", "Developer-only read-only extraction. No model calls. Costs are omitted; no pricing source was supplied.", "",
             "An observation stop is not Queen collapse or completion of the research request. Connections are offers; they do not prove uptake or novelty.", "",
             "## Saved stop and run state", "", block(json.dumps({"requests": state["tables"].get("hive_requests"), "runs": [{k: v for k, v in row.items() if k != "config"} for row in state["tables"].get("hive_runs", [])], "result": obj(report.get("saved_result")).get("value"), "launcher": obj(report.get("launcher_receipt")).get("value")}, ensure_ascii=False, indent=2)), "",
             "## Teams, memberships and connections", "", block(json.dumps(summary, ensure_ascii=False, indent=2)), "",
             "## Token usage", "", "Cached input is a subset of input; reasoning is a subset of output. Missing counters remain unknown.", "", block(json.dumps(report["usage_totals"], indent=2)), "",
             "## Rejections and failures", "", "These are diagnostic events, not unique calls. A single failure may produce several events.", "", block(json.dumps(debug["failure_events"], ensure_ascii=False, indent=2)), "",
             "## Operational chronology", "", "Work IDs order committed work. Cross-table rows are ordered by saved timestamps; equal timestamps do not prove an ordering.", ""]
    for item in report["evidence"]["chronology"]:
        row = item["record"]
        lines += [f"### {item['source_table']} · {row.get('id', str(row.get('inquiry_id')) + '/' + str(row.get('drone_id')))}", "", block(json.dumps(row, ensure_ascii=False, indent=2)), ""]
    lines += ["## Exact model exchanges", ""]
    for call in debug["calls"]:
        lines += [f"### {call['call_id']}", "", block(json.dumps({key: call.get(key) for key in ("drone_id", "phase", "model", "outcome", "http_status", "duration_ms", "token_counters", "error", "refusal", "action_events")}, ensure_ascii=False, indent=2)), "", "#### Request (saved provider payload)", "", block(call.get("request_raw")), "", "#### Raw response", "", block(call.get("response_raw")), "", "#### Reply", "", block(call.get("reply")), ""]
    lines += ["## Extraction limits", "", "SQLite connections use mode=ro. State and diagnostic databases have separate read snapshots. Missing tables or files and call-association anomalies are explicit in JSON. No diagnostic material is sent to agents.", "", block(json.dumps({"state_available": state["available"], "missing_tables": state["missing_tables"], "debug_available": debug["available"], "association_anomalies": debug["association_anomalies"]}, ensure_ascii=False, indent=2)), ""]
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("run_folder", type=Path)
    parser.add_argument("output_prefix", type=Path)
    args = parser.parse_args()
    run, prefix = args.run_folder.resolve(), args.output_prefix.resolve()
    outputs = [Path(str(prefix) + suffix) for suffix in (".json", ".md")]
    if any(path.exists() for path in outputs):
        raise FileExistsError("Preserve existing analysis outputs; use a new prefix")
    if not prefix.parent.is_dir():
        raise FileNotFoundError("Output parent must already exist")
    config = json.loads((run / "config.json").read_text(encoding="utf-8-sig"))
    state = load_state(local_file(run, config["files"]["state"]))
    debug = load_debug(local_file(run, config["files"]["debug"]))
    evidence = evidence_summary(state)
    groups = collections.defaultdict(list)
    for call in debug["calls"]:
        groups[str(call.get("model"))].append(call)
    summary = {"state_row_counts": {table: len(state["tables"][table]) if table in state["tables"] else None for table in TABLES},
               "teams": evidence["teams"], "overlapping_memberships": evidence["overlapping_memberships"],
               "connection_work_ids": [row["id"] for row in evidence["connections"]],
               "observer_qualified_team_count": sum(team["observer_qualified"] for team in evidence["teams"]) if not state["missing_tables"] else None,
               "provider_call_count": len(debug["calls"]) if debug["available"] else None,
               "failure_event_counts": dict(collections.Counter(event["category"] for event in debug["failure_events"]))}
    report = {"run_folder": str(run), "extracted_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "state": state, "debug": debug, "evidence": evidence, "summary": summary,
              "usage_totals": totals(debug["calls"]) if debug["available"] else None,
              "usage_by_model": {model: totals(calls) for model, calls in groups.items()},
              "saved_result": saved_json(run, "inquiry-result.json"), "launcher_receipt": saved_json(run, "inquiry.receipt.json"),
              "cost_status": "omitted: no pricing source supplied; provider billed dollars unavailable"}
    with outputs[0].open("x", encoding="utf-8", newline="\n") as stream:
        json.dump(report, stream, ensure_ascii=False, indent=2, allow_nan=False)
    with outputs[1].open("x", encoding="utf-8", newline="\n") as stream:
        stream.write(markdown(report))
    print(json.dumps({"provider_calls": summary["provider_call_count"], "observer_qualified_teams": summary["observer_qualified_team_count"],
                      "connections": len(evidence["connections"]), "failure_events": summary["failure_event_counts"],
                      "usage": report["usage_totals"], "json": str(outputs[0]), "markdown": str(outputs[1])}, ensure_ascii=False))


if __name__ == "__main__":
    main()
