"""Read-only v17 resource-review evidence. Never starts, pauses, or changes a hive."""
import argparse
import collections
import datetime
import json
import sqlite3
import sys
from pathlib import Path


def utc_now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def instant(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def records(db, query, args=()):
    return [dict(row) for row in db.execute(query, args)]


def local_file(folder, name):
    if not isinstance(name, str) or not name or name in (".", "..") or "/" in name or "\\" in name:
        raise ValueError("configured database filename must be flat")
    return folder / name


def readonly(path):
    db = sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True)
    db.row_factory = sqlite3.Row
    db.execute("BEGIN")
    return db


def work_ref(row):
    if row is None:
        return None
    return {key: row[key] for key in ("id", "inquiry_id", "drone_id", "phase", "kind", "created", "body_excerpt")}


def read_state(path):
    db = readonly(path)
    try:
        request = db.execute("SELECT id,status,finish_reason,created,criteria_met FROM hive_requests ORDER BY id DESC LIMIT 1").fetchone()
        if request is None:
            raise ValueError("state has no saved request")
        request = dict(request)
        request_id = request["id"]
        captured = utc_now()
        work = records(db, """SELECT w.id,w.inquiry_id,w.target_inquiry_id,w.drone_id,w.phase,w.kind,w.created,
            substr(w.body,1,240) AS body_excerpt FROM hive_inquiry_work w
            JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE i.request_id=? ORDER BY w.id""", (request_id,))
        members = records(db, """SELECT m.inquiry_id,m.drone_id,m.created FROM hive_inquiry_members m
            JOIN hive_inquiries i ON i.id=m.inquiry_id WHERE i.request_id=? ORDER BY m.inquiry_id,m.drone_id""", (request_id,))
        actors = records(db, """SELECT q.drone_id,q.owner,q.phase,q.focus_id,q.omit_oversized,d.created
            FROM hive_worker_queue q JOIN hive_drones d ON d.id=q.drone_id
            WHERE q.request_id=? ORDER BY q.drone_id""", (request_id,))
        inquiries = records(db, "SELECT id,question,proposed_by,created FROM hive_inquiries WHERE request_id=? ORDER BY id", (request_id,))
        queen = records(db, "SELECT * FROM hive_worker_queen WHERE request_id=?", (request_id,))
        resources = records(db, "SELECT * FROM hive_worker_resources WHERE request_id=?", (request_id,))
        if resources:
            resources[0]["selected_ids"] = json.loads(resources[0]["selected_ids"])
        runs = records(db, "SELECT id,started,finished,status,calls FROM hive_runs WHERE request_id=? ORDER BY id", (request_id,))
    finally:
        db.close()
    memberships = {(r["inquiry_id"], r["drone_id"]) for r in members}
    pc = {(r["inquiry_id"], r["drone_id"]) for r in work if r["kind"] in ("PROPOSE", "CONTRIBUTE")} & memberships
    pjc = {(r["inquiry_id"], r["drone_id"]) for r in work if r["kind"] in ("PROPOSE", "JOIN", "CONTRIBUTE")} & memberships
    pc_counts = collections.Counter(r["inquiry_id"] for r in work if r["kind"] in ("PROPOSE", "CONTRIBUTE") and (r["inquiry_id"], r["drone_id"]) in memberships)
    for inquiry in inquiries:
        inquiry["question_first_line"] = inquiry.pop("question").split("\n", 1)[0]
        inquiry_id = inquiry["id"]
        inquiry["committed_member_ids"] = [r["drone_id"] for r in members if r["inquiry_id"] == inquiry_id]
        inquiry["non_join_contributor_ids"] = sorted(d for i, d in pc if i == inquiry_id)
        inquiry["join_inclusive_contributor_ids"] = sorted(d for i, d in pjc if i == inquiry_id)
        inquiry["non_join_work_count"] = pc_counts[inquiry_id]
    births = [r for r in work if r["kind"] == "SPAWN"]
    state = {
        "source_db": str(path), "snapshot_utc": captured, "request": request, "runs": runs,
        "boundary": {"latest_work_id": work[-1]["id"] if work else 0, "work_count": len(work)},
        "population": len(actors), "seed_count": sum(r["omit_oversized"] is None for r in actors),
        "newborn_cohorts": {"ordinary": sum(r["omit_oversized"] == 0 for r in actors),
                            "omit_selected": sum(r["omit_oversized"] == 1 for r in actors)},
        "successful_spawn_count": len(births), "successful_spawns": [work_ref(r) for r in births],
        "action_counts": dict(collections.Counter(r["kind"] for r in work)),
        "non_join_contributing_pairs": [{"inquiry_id": i, "drone_id": d} for i, d in sorted(pc)],
        "non_join_working_inquiry_count": sum(len(i["non_join_contributor_ids"]) >= 2 for i in inquiries),
        "join_inclusive_working_inquiry_count": sum(len(i["join_inclusive_contributor_ids"]) >= 2 for i in inquiries),
        "inquiries": inquiries,
        "owned_drone_claims": [r for r in actors if r["owner"]],
        "current_queen": queen[0] if queen else None,
        "current_resource_selection": resources[0] if resources else None,
    }
    return state, actors, work


def user_input(payload):
    for message in payload.get("messages", []):
        if message.get("role") == "user" and isinstance(message.get("content"), str):
            try:
                value = json.loads(message["content"])
            except json.JSONDecodeError:
                return None
            return value if isinstance(value, dict) else None
    return None


def call_key(row, fields):
    return (fields.get("worker", fields.get("worker_id")), row["scope"], row["drone_id"], fields.get("phase"))


def read_debug(path, request_id):
    db = readonly(path)
    try:
        last = db.execute("SELECT id,ts FROM hive_log ORDER BY id DESC LIMIT 1").fetchone()
        result = {"source_db": str(path), "snapshot_utc": utc_now(), "boundary": dict(last) if last else None,
                  "resource_calls": [], "resource_decisions": [], "resource_replies": [], "newborn_menus": [],
                  "spawn_receipts": [], "errors": [], "unmatched_finishes": [], "overlapping_call_keys": []}
        active, last_resource_call, pending_menus, last_drone_calls = {}, {}, {}, {}
        starts = finishes = 0
        finish_counts, http_counts, error_events = collections.Counter(), collections.Counter(), collections.Counter()
        query = """SELECT id,ts,event,scope,drone_id,model,outcome,
            CASE WHEN event='llm.response' THEN json_remove(details,'$.body') ELSE details END AS details
            FROM hive_log WHERE request_id=? AND
            (event IN ('llm.start','llm.finish','llm.response','newborn_menu','worker_resource_decision','worker_resource_reply',
            'worker_action_saved','inquiry_action_rejected','worker_pool_finished','worker_exploration_finished')
            OR outcome IN ('error','format_error','refusal')) ORDER BY id"""
        for saved in db.execute(query, (request_id,)):
            row = dict(saved)
            fields = json.loads(row.pop("details"))
            event, log_id = row["event"], row["id"]
            key = call_key(row, fields)
            if event == "llm.start":
                starts += 1
                payload = json.loads(fields["request"])
                value = user_input(payload)
                call = {"start_log_id": log_id, "started": row["ts"], "scope": row["scope"],
                        "worker": key[0], "phase": key[3], "model": row["model"]}
                if key in active:
                    result["overlapping_call_keys"].append({"prior_start": active[key]["start_log_id"], "new_start": log_id})
                if row["scope"] == "drone":
                    last_drone_calls[(key[0], row["drone_id"], key[3])] = call
                    menu = pending_menus.pop(row["drone_id"], None)
                    if menu and menu["phase"] == key[3] and menu["worker"] == key[0]:
                        cards = (value or {}).get("inquiry_signals")
                        actual_ids = [card.get("id") for card in cards] if isinstance(cards, list) else None
                        actor_id = ((value or {}).get("drone") or {}).get("id")
                        call.update({"actual_signal_ids": actual_ids,
                            "actual_needs_fit_choice": (value or {}).get("needs_fit_choice"),
                            "matches_logged_menu": bool(value and value.get("needs_fit_choice") is True
                                and actor_id == row["drone_id"] and value.get("phase") == key[3]
                                and actual_ids == menu["shown_inquiry_ids"]),
                            "logged_omitted_ids_absent": actual_ids is not None and not bool(set(actual_ids) & set(menu["omitted_inquiry_ids"]))})
                        menu["matched_model_call"] = call
                if value and value.get("purpose") == "resource_review":
                    call["resource_input"] = {k: value.get(k) for k in ("successful_births", "births_reviewed", "root_inquiry_id", "prior_selection")}
                    call["resource_input"]["inquiries"] = [
                        {k: inquiry.get(k) for k in ("id", "question_first_line", "committed_member_count", "contributing_member_count", "work_record_count", "work_since_review")}
                        | {"latest_work_id": (inquiry.get("latest_work") or {}).get("id")}
                        for inquiry in value.get("inquiries", [])]
                    result["resource_calls"].append(call)
                    last_resource_call[key[0]] = call
                active[key] = call
            elif event == "llm.response":
                call = active.get(key)
                if call is not None:
                    call.setdefault("http_responses", []).append({"log_id": log_id, "status_code": fields.get("status_code"),
                        "attempt": fields.get("attempt"), "body_complete": fields.get("body_complete")})
            elif event == "llm.finish":
                finishes += 1
                outcome = fields.get("outcome", row["outcome"])
                finish_counts[str(outcome)] += 1
                call = active.pop(key, None)
                if call is None:
                    result["unmatched_finishes"].append(log_id)
                else:
                    call.update({"finish_log_id": log_id, "finished": row["ts"], "outcome": outcome,
                                 "attempts": fields.get("attempts"), "duration_ms": fields.get("duration_ms")})
                    if "resource_input" in call:
                        call.update({"reply": fields.get("reply"), "error": fields.get("error"), "refusal": fields.get("refusal"),
                                     "status_code": fields.get("status_code")})
            elif event == "worker_resource_reply":
                result["resource_replies"].append({"log_id": log_id, "created": row["ts"], **fields})
            elif event == "worker_resource_decision":
                call = last_resource_call.get(key[0])
                result["resource_decisions"].append({"log_id": log_id, "created": row["ts"],
                    "call_start_log_id": call["start_log_id"] if call else None,
                    "successful_births": fields["successful_births"], "outcome": fields.get("outcome"),
                    "action": fields["action"]})
            elif event == "newborn_menu":
                menu = {"log_id": log_id, "created": row["ts"], "drone_id": row["drone_id"], "worker": key[0],
                    "phase": fields.get("phase"), "omit_oversized": fields["omit_oversized"], "discovered": fields["discovered"],
                    "shown_inquiry_ids": fields["shown_inquiry_ids"], "omitted_inquiry_ids": fields["omitted_inquiry_ids"],
                    "matched_model_call": None}
                result["newborn_menus"].append(menu)
                # Replacing the pending menu prevents correlating across a later
                # input preparation for this actor, even if no call followed it.
                pending_menus[row["drone_id"]] = menu
            elif event == "worker_action_saved":
                call = last_drone_calls.get((key[0], row["drone_id"], key[3]))
                if call and "finish_log_id" in call and "accepted_action" not in call:
                    action = fields["action"]
                    call["accepted_action"] = {"log_id": log_id, "created": row["ts"], "work_id": fields["work_id"],
                        "kind": action["kind"], "inquiry_id": action.get("inquiry_id"), "target_id": action.get("target_id"),
                        "body_excerpt": action.get("body", "")[:240], "relevance": action.get("relevance"),
                        "inquiry_value": action.get("inquiry_value")}
                if fields.get("spawned_drone_id"):
                    result["spawn_receipts"].append({"log_id": log_id, "created": row["ts"], "parent_drone_id": row["drone_id"],
                        "drone_id": fields["spawned_drone_id"], "work_id": fields["work_id"], "omit_oversized": fields["omit_oversized"],
                        "invited_inquiry_id": fields["action"].get("inquiry_id")})
            error = fields.get("error") or (fields.get("result") or {}).get("error")
            if error or event == "inquiry_action_rejected" or fields.get("outcome") in ("error", "format_error", "refusal"):
                error_events[event] += 1
                result["errors"].append({"log_id": log_id, "created": row["ts"], "event": event, "scope": row["scope"],
                    "drone_id": row["drone_id"], "phase": fields.get("phase"), "error": error,
                    "kind": fields.get("kind"), "status_code": fields.get("status_code"), "outcome": fields.get("outcome"),
                    "body": fields.get("body") if event == "inquiry_action_rejected" else None,
                    "refusal": fields.get("refusal")})
        # Read status metadata without exporting full provider response bodies.
        for row in db.execute("""SELECT json_extract(details,'$.status_code') AS code,COUNT(*) AS total
            FROM hive_log WHERE request_id=? AND event='llm.response' GROUP BY code""", (request_id,)):
            http_counts[str(row["code"])] = row["total"]
        result.update({"call_start_count": starts, "call_finish_count": finishes, "finish_outcomes": dict(finish_counts),
                       "http_response_status_counts": dict(http_counts), "error_event_counts": dict(error_events),
                       "in_flight_calls": list(active.values())})
    finally:
        db.close()
    return result


def combine(state, actors, work, debug):
    work_ids = {row["id"] for row in work}
    menus = collections.defaultdict(list)
    by_actor = collections.defaultdict(list)
    for row in debug["newborn_menus"]:
        menus[row["drone_id"]].append(row)
    for row in work:
        by_actor[row["drone_id"]].append(row)
    births = {row["drone_id"]: row for row in debug["spawn_receipts"] if row["work_id"] in work_ids}
    decisions = [row for row in debug["resource_decisions"] if row["outcome"] == "saved"]
    newborns, revelations = [], []
    for actor in actors:
        if actor["omit_oversized"] is None:
            continue
        drone_id = actor["drone_id"]
        actor_menus, actor_work = menus[drone_id], by_actor[drone_id]
        first = actor_menus[0] if actor_menus else None
        after_first = [row for row in actor_work if first and instant(row["created"]) >= instant(first["created"])]
        first_join = next((row for row in after_first if row["kind"] == "JOIN"), None)
        first_pc = next((row for row in after_first if row["kind"] in ("PROPOSE", "CONTRIBUTE")), None)
        first_contribute = next((row for row in after_first if row["kind"] == "CONTRIBUTE"), None)
        prior_review = next((row for row in reversed(decisions) if first and row["log_id"] < first["log_id"]), None)
        formerly_omitted = set()
        for menu in actor_menus:
            revealed = formerly_omitted & set(menu["shown_inquiry_ids"]) if menu["discovered"] else set()
            if revealed:
                discover = next((row for row in actor_work if row["kind"] == "DISCOVER" and instant(row["created"]) <= instant(menu["created"])), None)
                revelations.append({"drone_id": drone_id, "menu_log_id": menu["log_id"], "revealed_inquiry_ids": sorted(revealed),
                                    "discover_work": work_ref(discover)})
                break
            formerly_omitted.update(menu["omitted_inquiry_ids"])
        newborns.append({"drone_id": drone_id, "created": actor["created"], "omit_oversized": bool(actor["omit_oversized"]),
            "birth_receipt": births.get(drone_id), "menu_count": len(actor_menus), "first_menu": first,
            "repeated_menu_count": max(0, len(actor_menus) - 1),
            "menus_with_actual_omission": sum(bool(row["omitted_inquiry_ids"]) for row in actor_menus),
            "first_menu_after_review_log_id": prior_review["log_id"] if prior_review else None,
            "first_join": work_ref(first_join), "first_non_join_contribution": work_ref(first_pc),
            "first_contribute": work_ref(first_contribute),
            "join_to_first_menu_omitted": bool(first_join and first_join["inquiry_id"] in first["omitted_inquiry_ids"]),
            "current_focus_id": actor["focus_id"]})
    post_review = [row for row in newborns if row["first_menu_after_review_log_id"]]
    masked = [row for row in post_review if row["first_menu"]["omitted_inquiry_ids"]]
    unmasked = [row for row in post_review if not row["omit_oversized"]]
    resource_calls = debug["resource_calls"]
    matched_menus = [row for row in debug["newborn_menus"] if row.get("matched_model_call")]
    actual_masked = [row for row in matched_menus if row["omitted_inquiry_ids"]
        and row["matched_model_call"]["matches_logged_menu"] and row["matched_model_call"]["logged_omitted_ids_absent"]]
    masked_answered = [row for row in actual_masked if row["matched_model_call"].get("outcome") == "answered"]
    summary = {
        "request_status": state["request"]["status"], "finish_reason": state["request"]["finish_reason"],
        "population": state["population"], "successful_births": state["successful_spawn_count"],
        "non_join_working_inquiries": state["non_join_working_inquiry_count"],
        "non_join_contributing_pair_count": len(state["non_join_contributing_pairs"]),
        "accepted_resource_reviews": len(decisions), "review_birth_snapshots": [row["successful_births"] for row in decisions],
        "resource_call_starts": len(resource_calls), "resource_call_finishes": sum("finish_log_id" in row for row in resource_calls),
        "resource_answered_calls": sum(row.get("outcome") == "answered" for row in resource_calls),
        "matched_newborn_model_call_count": len(matched_menus),
        "menus_without_matched_model_call": len(debug["newborn_menus"]) - len(matched_menus),
        "menu_model_mismatch_count": sum(not row["matched_model_call"]["matches_logged_menu"] for row in matched_menus),
        "actual_masked_model_calls": len(actual_masked), "actual_masked_calls_answered": len(masked_answered),
        "actual_masked_answered_drone_count": len({row["drone_id"] for row in masked_answered}),
        "actual_masked_answered_with_accepted_action": sum("accepted_action" in row["matched_model_call"] for row in masked_answered),
        "cohort_counts": state["newborn_cohorts"], "newborns_with_first_menu": sum(row["first_menu"] is not None for row in newborns),
        "newborns_first_menu_actually_masked": sum(bool(row["first_menu"] and row["first_menu"]["omitted_inquiry_ids"]) for row in newborns),
        "newborns_any_menu_actually_masked": sum(row["menus_with_actual_omission"] > 0 for row in newborns),
        "post_review_first_menus": len(post_review), "post_review_masked_first_menus": len(masked),
        "post_review_ordinary_cohort_first_menus": len(unmasked),
        "post_review_masked_with_later_join": sum(row["first_join"] is not None for row in masked),
        "post_review_ordinary_with_later_join": sum(row["first_join"] is not None for row in unmasked),
        "post_review_masked_with_later_contribute": sum(row["first_contribute"] is not None for row in masked),
        "post_review_ordinary_with_later_contribute": sum(row["first_contribute"] is not None for row in unmasked),
        "discover_revelations": len(revelations), "owned_drone_claims": len(state["owned_drone_claims"]),
        "queen_owned": bool((state["current_queen"] or {}).get("owner")),
        "calls_started": debug["call_start_count"], "calls_finished": debug["call_finish_count"],
        "in_flight_calls": len(debug["in_flight_calls"]), "http_status_counts": debug["http_response_status_counts"],
        "error_event_counts": debug["error_event_counts"],
    }
    return {"summary": summary, "state": state, "debug": debug, "newborn_choices": newborns, "discover_revelations": revelations,
            "limits": [
                "State and diagnostics are separate read transactions, not one atomic cross-database snapshot; newer diagnostic actions may not yet be in the state snapshot.",
                "Latest preceding resource decision log identifies logged chronology, not a strict causal boundary: menu preparation and decision logging can overlap.",
                "Cohort membership is not actual masking. Actual masking requires a nonempty omitted_inquiry_ids list; first menus and repeated menus are reported separately.",
                "A matched model call is the following same-worker, same-drone, same-phase request before another menu; its parsed user inquiry IDs are compared exactly. HTTP response and answered finish establish provider receipt beyond input preparation alone.",
                "Non-JOIN contribution means saved PROPOSE or CONTRIBUTE by a committed member. These counts do not establish depth, intellectual influence, novelty, or research validity.",
                "Local relevance and inquiry_value scores belong to actions and resource decisions, not parent hypothesis grades. Missing observations remain absent.",
                "Recorded error events may describe the same failed call at several layers; event totals are not a count of unique failures.",
            ]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    folder, output = args.run.resolve(), args.out.resolve()
    if output.exists():
        raise FileExistsError(f"refusing to overwrite {output}")
    config = json.loads((folder / "config.json").read_text(encoding="utf-8-sig"))
    files = config["files"]
    state, actors, work = read_state(local_file(folder, files["state"]))
    debug = read_debug(local_file(folder, files["debug"]), state["request"]["id"])
    result = combine(state, actors, work, debug)
    result.update({"analyzed_utc": utc_now(), "run_folder": str(folder), "configured_resource_review_every": config["workers"]["resource_review_every"],
                   "configured_newborn_omit_probability": config["workers"]["newborn_omit_probability"]})
    with output.open("x", encoding="utf-8", newline="\n") as stream:
        json.dump(result, stream, ensure_ascii=False, indent=2, allow_nan=False)
        stream.write("\n")
    print(json.dumps({"output": str(output), **result["summary"]}, ensure_ascii=False, allow_nan=False))


if __name__ == "__main__":
    try:
        main()
    except (OSError, sqlite3.Error, ValueError, KeyError, TypeError) as error:
        print(json.dumps({"analysis_error": str(error)}, ensure_ascii=False), file=sys.stderr)
        sys.exit(1)
