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
    "hive_inquiry_members", "hive_inquiry_work", "hive_worker_queue",
    "hive_worker_queen", "hive_worker_queen_seen", "hive_worker_guidance",
)
TABLE_ORDER = {"hive_inquiry_members": "inquiry_id,drone_id", "hive_worker_queue": "drone_id",
               "hive_worker_queen": "request_id", "hive_worker_queen_seen": "request_id,inquiry_id"}
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
            order = TABLE_ORDER.get(table, "id")
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


def worker_key(event):
    worker = event.get("worker")
    if not isinstance(worker, str) or not worker:
        return None
    return worker, event.get("run_id"), event.get("request_id")


def same_actor(event, call):
    # A Queen has no drone_id. Equality, including that intentional null,
    # matters; a different worker's latest call must never be substituted.
    return call is not None and worker_key(event) is not None and all(
        event.get(key) == call.get(key)
        for key in ("worker", "run_id", "request_id", "drone_id", "phase")
    )


def request_sizes(call):
    raw = call.get("request_raw")
    messages = obj(call.get("request")).get("messages")
    result = {"provider_payload_utf8_bytes": len(raw.encode("utf-8")) if isinstance(raw, str) else None,
              "message_count": None, "message_text_chars": None, "message_text_utf8_bytes": None,
              "non_text_contents": None, "by_role": {}}
    if isinstance(messages, list):
        result["message_count"] = len(messages)
        non_text = 0
        by_role = collections.defaultdict(lambda: {"chars": 0, "utf8_bytes": 0})
        for message in messages:
            content = obj(message).get("content")
            if not isinstance(content, str):
                non_text += 1
                continue
            role = str(obj(message).get("role"))
            by_role[role]["chars"] += len(content)
            by_role[role]["utf8_bytes"] += len(content.encode("utf-8"))
        result["non_text_contents"], result["by_role"] = non_text, dict(by_role)
        if non_text == 0:
            result["message_text_chars"] = sum(value["chars"] for value in by_role.values())
            result["message_text_utf8_bytes"] = sum(value["utf8_bytes"] for value in by_role.values())
    return result


def load_debug(path):
    result = {"source_db": str(path), "available": path.is_file(), "calls": [], "events": [],
              "failure_events": [], "association_anomalies": [], "event_counts": [], "sql_counts": []}
    if not result["available"]:
        return result
    active, latest, ambiguous = {}, {}, set()
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
                         details_raw=row["details"], details_parse_error=error, phase=data.get("phase"),
                         worker=data.get("worker") or data.get("worker_id") or data.get("owner"))
            kind = row["event"]
            key = worker_key(event)
            identity = {name: event.get(name) for name in ("worker", "run_id", "request_id", "drone_id", "phase", "scope")}
            event["associated_call_id"] = None
            if error:
                result["association_anomalies"].append({"log_id": row["id"], "kind": "invalid_log_details", "error": error})
            if kind == "llm.start":
                request, parse_error = decode(data.get("request"))
                call = dict(identity, call_id=f"{path}:hive_log:{row['id']}", source_db=str(path), log_table="hive_log",
                            start_log_id=row["id"], started=row["ts"], requested_model=data.get("model"), model=data.get("model"),
                            request=request, request_raw=data.get("request"), request_parse_error=parse_error,
                            responses=[], outcome="incomplete", action_events=[])
                result["calls"].append(call)
                event["associated_call_id"] = call["call_id"]
                if key is None:
                    result["association_anomalies"].append({"log_id": row["id"], "kind": "start_missing_worker", "call_id": call["call_id"]})
                else:
                    if key in active:
                        ambiguous.add(key)
                        active[key]["association_ambiguous"] = True
                        result["association_anomalies"].append({"log_id": row["id"], "kind": "same_worker_start_before_finish", "worker": event["worker"], "previous_call": active[key]["call_id"]})
                    if key in ambiguous:
                        call["association_ambiguous"] = True
                    active[key] = call
                    latest.pop(key, None)
            elif kind == "llm.response":
                body, parse_error = decode(data.get("body"))
                response = {"log_id": row["id"], "ts": row["ts"], "http_status": data.get("status_code"),
                            "body": body, "body_raw": data.get("body"), "parse_error": parse_error}
                call = active.get(key)
                if key in ambiguous or not same_actor(event, call):
                    result["association_anomalies"].append(dict(response, kind="unassociated_response", worker=event["worker"]))
                else:
                    call["responses"].append(response)
                    call.update(response=body, response_raw=data.get("body"), http_status=data.get("status_code"))
                    event["associated_call_id"] = call["call_id"]
            elif kind == "llm.finish":
                call = active.get(key)
                if key in ambiguous or not same_actor(event, call):
                    result["association_anomalies"].append({"log_id": row["id"], "kind": "unassociated_finish", "worker": event["worker"], "details": data})
                else:
                    call.update(finish_log_id=row["id"], finished=row["ts"], outcome=data.get("outcome"),
                                duration_ms=data.get("duration_ms"), usage=data.get("usage"), reply=data.get("reply"),
                                refusal=data.get("refusal"), error=data.get("error"), provider_error_kind=data.get("kind"),
                                model=obj(call.get("response")).get("model") or data.get("model") or call["model"])
                    if call.get("http_status") is None and data.get("status_code"):
                        call["http_status"] = data["status_code"]
                    event["associated_call_id"] = call["call_id"]
                    latest[key] = active.pop(key)
            elif kind in ("call_observed", "worker_claim"):
                # A new operation may fail before llm.start. Do not blame its
                # error on this worker's previous successful provider call.
                latest.pop(key, None)
            elif kind in ("inquiry_reply", "inquiry_action", "inquiry_action_rejected", "inquiry_pass",
                          "worker_action_saved", "worker_action_rejected", "worker_queen_reply", "worker_queen_decision"):
                call = latest.get(key)
                if key not in ambiguous and same_actor(event, call):
                    event["associated_call_id"] = call["call_id"]
                    call["action_events"].append({"log_id": row["id"], "event": kind, "details": data})
            category = None
            if kind == "llm.finish" and (data.get("error") or data.get("refusal") or data.get("outcome") == "refusal"):
                category = "provider"
            elif kind == "llm.response" and isinstance(data.get("status_code"), int) and not 200 <= data["status_code"] < 300:
                category = "provider_http"
            elif kind == "worker_action_rejected":
                category = "action_application"
            elif kind == "inquiry_action_rejected":
                if data.get("outcome") == "format_error" or data.get("body") is not None:
                    category = "action_parse"
                elif data.get("outcome") == "action_error":
                    category = "action_application"
                else:
                    category = "action_rejected"
            elif data.get("error"):
                category = "runtime"
            if category:
                result["failure_events"].append(dict(event, category=category))
            if kind.startswith("llm.") or kind.startswith("inquiry_") or kind.startswith("worker_") or category:
                result["events"].append(event)
    for call in result["calls"]:
        token_usage(call)
        call["request_sizes"] = request_sizes(call)
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


def context_totals(calls):
    result = {}
    for name in ("provider_payload_utf8_bytes", "message_text_chars", "message_text_utf8_bytes"):
        known = [call["request_sizes"][name] for call in calls if integer(call["request_sizes"][name])]
        result[name] = {"minimum": min(known) if known else None, "maximum": max(known) if known else None,
                        "mean": sum(known) / len(known) if known else None, "unknown_calls": len(calls) - len(known)}
    return result


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
    for table in ("hive_inquiries", "hive_inquiry_members", "hive_inquiry_work", "hive_worker_guidance"):
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


def saved_text(run, name):
    path = run / name
    if not path.is_file():
        return None
    return {"path": str(path), "raw": path.read_text(encoding="utf-8-sig")}


def block(text):
    text = "" if text is None else str(text)
    fence = "````"
    while fence in text:
        fence += "`"
    return f"{fence}\n{text}\n{fence}"


def markdown(report):
    state, debug, summary = report["state"], report["debug"], report["summary"]
    lines = ["# Worker inquiry observation", "", "Developer-only read-only extraction. No model calls. Costs are omitted; no pricing source was supplied.", "",
             "An observation stop is not Queen collapse or completion of the research request. Connections are offers; they do not prove uptake or novelty.", "",
             "## Saved stop and run state", "", block(json.dumps({"requests": state["tables"].get("hive_requests"), "runs": [{k: v for k, v in row.items() if k != "config"} for row in state["tables"].get("hive_runs", [])], "results": [{"path": item["path"], "value": item["value"], "parse_error": item["parse_error"]} for item in report["saved_results"]], "launcher": obj(report.get("launcher_receipt")).get("value"), "worker_queue": state["tables"].get("hive_worker_queue"), "queen": state["tables"].get("hive_worker_queen"), "queen_checkpoints": state["tables"].get("hive_worker_queen_seen")}, ensure_ascii=False, indent=2)), "",
             "## Teams, memberships and connections", "", block(json.dumps(summary, ensure_ascii=False, indent=2)), "",
             "## Token usage", "", "Cached input is a subset of input; reasoning is a subset of output. Missing counters remain unknown.", "", block(json.dumps(report["usage_totals"], indent=2)), "",
             "## Input context sizes", "", "These are measured saved-payload bytes and decoded message-text characters/bytes, not estimated token counts. Provider input tokens remain separate. Non-text content leaves complete text size unknown.", "", block(json.dumps(report["context_size_totals"], indent=2)), "",
             "## Rejections and failures", "", "These are diagnostic events, not unique calls. A single failure may produce several events.", "", block(json.dumps(debug["failure_events"], ensure_ascii=False, indent=2)), "",
             "## Launcher output", "", block(json.dumps(report["saved_output"], ensure_ascii=False, indent=2)), "",
             "## Operational chronology", "", "Work IDs order committed work. Cross-table rows are ordered by saved timestamps; equal timestamps do not prove an ordering.", ""]
    for item in report["evidence"]["chronology"]:
        row = item["record"]
        lines += [f"### {item['source_table']} · {row.get('id', str(row.get('inquiry_id')) + '/' + str(row.get('drone_id')))}", "", block(json.dumps(row, ensure_ascii=False, indent=2)), ""]
    lines += ["## Exact model exchanges", ""]
    for call in debug["calls"]:
        lines += [f"### {call['call_id']}", "", block(json.dumps({key: call.get(key) for key in ("worker", "run_id", "request_id", "drone_id", "phase", "model", "outcome", "http_status", "duration_ms", "request_sizes", "token_counters", "error", "refusal", "action_events")}, ensure_ascii=False, indent=2)), "", "#### Request (saved provider payload)", "", block(call.get("request_raw")), "", "#### Raw response", "", block(call.get("response_raw")), "", "#### Reply", "", block(call.get("reply")), ""]
    lines += ["## Extraction limits", "", "SQLite connections use mode=ro. State and diagnostic databases have separate read snapshots. Calls are associated only within a worker and matching run/request/drone/phase context; a Queen has no drone ID. Missing identities, conflicting same-worker calls and unmatched responses remain explicit anomalies. Summed provider duration is not elapsed wall time because calls overlap. Missing usage is not zero or a free failed call. No diagnostic material is sent to agents.", "", block(json.dumps({"state_available": state["available"], "missing_tables": state["missing_tables"], "debug_available": debug["available"], "association_anomalies": debug["association_anomalies"]}, ensure_ascii=False, indent=2)), ""]
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
    groups, workers, phases = collections.defaultdict(list), collections.defaultdict(list), collections.defaultdict(list)
    for call in debug["calls"]:
        groups[str(call.get("model"))].append(call)
        workers[str(call.get("worker"))].append(call)
        phases[str(call.get("phase"))].append(call)
    spawns = [{"log_id": event["id"], "associated_call_id": event["associated_call_id"], "worker": event["worker"],
               "drone_id": event["drone_id"], "details": event["details"]}
              for event in debug["events"] if event["event"] == "worker_action_saved" and event["details"].get("spawned_drone_id") is not None]
    failed_calls = collections.defaultdict(set)
    unassociated_failures = collections.Counter()
    for event in debug["failure_events"]:
        if event["associated_call_id"] is not None:
            failed_calls[event["category"]].add(event["associated_call_id"])
        else:
            unassociated_failures[event["category"]] += 1
    required_team_tables = ("hive_inquiries", "hive_inquiry_members", "hive_inquiry_work")
    saved_results = [saved_json(run, path.name) for path in sorted(run.glob("worker-result-*.json")) if path.is_file()]
    legacy_result = saved_json(run, "inquiry-result.json")
    if legacy_result:
        saved_results.insert(0, legacy_result)
    summary = {"state_row_counts": {table: len(state["tables"][table]) if table in state["tables"] else None for table in TABLES},
               "teams": evidence["teams"], "overlapping_memberships": evidence["overlapping_memberships"],
               "connection_work_ids": [row["id"] for row in evidence["connections"]],
               "observer_qualified_team_count": sum(team["observer_qualified"] for team in evidence["teams"]) if all(table in state["tables"] for table in required_team_tables) else None,
               "provider_call_count": len(debug["calls"]) if debug["available"] else None,
               "worker_count_configured": obj(config.get("workers")).get("count"), "initial_drone_count": obj(config.get("hive")).get("drone_count"),
               "spawn_events": spawns, "spawned_drone_ids": sorted({event["details"]["spawned_drone_id"] for event in spawns}),
               "failure_event_counts": dict(collections.Counter(event["category"] for event in debug["failure_events"])),
               "unique_failed_calls_by_category": {category: len(ids) for category, ids in failed_calls.items()},
               "unique_failed_call_count": len(set().union(*failed_calls.values())),
               "unassociated_failure_events": dict(unassociated_failures)}
    report = {"analyzer_version": "0.0.10", "run_folder": str(run), "extracted_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "state": state, "debug": debug, "evidence": evidence, "summary": summary,
              "usage_totals": totals(debug["calls"]) if debug["available"] else None,
              "usage_by_model": {model: totals(calls) for model, calls in groups.items()},
              "usage_by_worker": {worker: totals(calls) for worker, calls in workers.items()},
              "usage_by_phase": {phase: totals(calls) for phase, calls in phases.items()},
              "context_size_totals": context_totals(debug["calls"]) if debug["available"] else None,
              "saved_results": saved_results, "saved_result": saved_results[-1] if saved_results else None,
              "launcher_receipt": saved_json(run, "workers.receipt.json") or saved_json(run, "inquiry.receipt.json"),
              "preparation_receipt": saved_json(run, "preparation-receipt.json"),
              "saved_output": {"stdout": saved_text(run, "workers.stdout.txt"), "stderr": saved_text(run, "workers.stderr.txt")},
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
