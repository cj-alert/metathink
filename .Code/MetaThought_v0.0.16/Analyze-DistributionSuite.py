"""Read-only distribution evidence at matched saved-work counts; never stops a run."""
import argparse
import collections
import datetime
import json
import sqlite3
from pathlib import Path


OBSERVER_KINDS = {"PROPOSE", "JOIN", "CONTRIBUTE"}
NON_JOIN_KINDS = {"PROPOSE", "CONTRIBUTE"}
FIT_OPS = {"worker_newborn_inquiries", "worker_fit_work"}


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def instant(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def load_json(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def local_file(folder, name):
    if not isinstance(name, str) or name in ("", ".", "..") or "/" in name or "\\" in name:
        raise ValueError("configured database filename must be flat")
    return folder / name


def readonly(path):
    db = sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True)
    db.row_factory = sqlite3.Row
    db.execute("BEGIN")
    return db


def records(db, query, values=()):
    return [dict(row) for row in db.execute(query, values)]


def call_key(row, details):
    return (details.get("worker", details.get("worker_id")), details.get("phase"), row["scope"], row["drone_id"])


def read_debug(path, request_id):
    data = {"available": False, "source_db": str(path), "births": [], "fit_queries": [], "fit_calls": [],
            "rejections": [], "provider_outcomes": {}, "call_starts": 0, "http_responses": {}}
    if not path.is_file():
        data["incomplete_reason"] = "debug database is missing"
        return data
    db = readonly(path)
    try:
        row = db.execute("SELECT id,ts FROM hive_log ORDER BY id DESC LIMIT 1").fetchone()
        data["boundary"] = dict(row) if row else None
        data["snapshot_utc"] = now()
        outcomes, responses, active = collections.Counter(), collections.Counter(), {}
        query = """SELECT id,ts,event,scope,request_id,drone_id,model,outcome,sql_op,rows_count,details
          FROM hive_log WHERE request_id=? AND
          (event IN('worker_action_saved','llm.start','llm.finish','llm.response','inquiry_action_rejected','worker_pool_finished')
          OR (event='sql_done' AND sql_op IN('worker_newborn_inquiries','worker_fit_work'))
          OR outcome IN('error','format_error','refusal')) ORDER BY id"""
        for saved in db.execute(query, (request_id,)):
            row = dict(saved)
            fields = json.loads(row.pop("details"))
            event = row["event"]
            if event == "worker_action_saved" and fields.get("spawned_drone_id"):
                data["births"].append({"log_id": row["id"], "created": row["ts"],
                    "work_id": fields.get("work_id"), "drone_id": fields["spawned_drone_id"],
                    "parent_drone_id": row["drone_id"], "newborn_mode": fields.get("newborn_mode"),
                    "initial_placement": fields.get("initial_placement")})
            elif event == "sql_done" and row["sql_op"] in FIT_OPS:
                data["fit_queries"].append(row)
            elif event == "llm.start":
                data["call_starts"] += 1
                exposure = None
                request = fields.get("request")
                try:
                    request = json.loads(request) if isinstance(request, str) else request
                    for message in (request or {}).get("messages", []):
                        if message.get("role") != "user" or not isinstance(message.get("content"), str):
                            continue
                        context = json.loads(message["content"])
                        if isinstance(context, dict) and context.get("needs_fit_choice") is True:
                            signals = context.get("inquiry_signals", [])
                            exposure = {"log_id": row["id"], "created": row["ts"], "drone_id": row["drone_id"],
                                "model": row["model"], "phase": fields.get("phase"),
                                "newborn_mode": context.get("newborn_mode"),
                                "offered_inquiry_ids": [s.get("id") for s in signals if isinstance(s, dict)],
                                "finish_log_id": None, "outcome": "unfinished"}
                            data["fit_calls"].append(exposure)
                            break
                except (ValueError, TypeError, AttributeError):
                    data.setdefault("unreadable_request_log_ids", []).append(row["id"])
                active[call_key(row, fields)] = exposure
            elif event == "llm.finish":
                outcomes[row["outcome"] or "unknown"] += 1
                exposure = active.pop(call_key(row, fields), None)
                if exposure is not None:
                    exposure.update(finish_log_id=row["id"], finished=row["ts"], outcome=row["outcome"], model=row["model"])
                if row["outcome"] in ("error", "refusal"):
                    data["rejections"].append({**row, "error": fields.get("error"), "kind": fields.get("kind")})
            elif event == "llm.response":
                responses[str(fields.get("status_code", "unknown"))] += 1
            elif event == "inquiry_action_rejected" or row["outcome"] in ("error", "format_error", "refusal"):
                data["rejections"].append({**row, "error": fields.get("error")})
            elif event == "worker_pool_finished":
                data["last_pool_result"] = {"log_id": row["id"], "created": row["ts"], "result": fields.get("result")}
        data.update(available=True, provider_outcomes=dict(outcomes), http_responses=dict(responses))
    finally:
        db.close()
    return data


def inspect(entry):
    folder = Path(entry["run_folder"]).resolve()
    result = {"test": entry.get("test"), "label": entry.get("label"), "run_folder": str(folder),
              "state_available": False, "incomplete_reasons": []}
    config_path = folder / "config.json"
    if not config_path.is_file():
        config_path = Path(entry.get("config_source", ""))
    try:
        if not config_path.is_file():
            raise FileNotFoundError("saved and source configuration are not available")
        config = load_json(config_path)
        result["config_read"] = str(config_path)
        result["newborn_mode"] = config.get("workers", {}).get("newborn_mode") or "invitation"
        state_path = local_file(folder, config["files"]["state"])
        debug_path = local_file(folder, config["files"]["debug"])
        result["state_db"] = str(state_path)
        if not state_path.is_file():
            raise FileNotFoundError("state database is missing or run has not started")
        db = readonly(state_path)
        try:
            request = db.execute("SELECT id,status,finish_reason FROM hive_requests ORDER BY id DESC LIMIT 1").fetchone()
            if request is None:
                raise ValueError("state exists but no request has started")
            result["request"] = dict(request)
            request_id = request["id"]
            result["state_snapshot_utc"] = now()
            result["work"] = records(db, """SELECT w.id,w.inquiry_id,w.target_inquiry_id,w.drone_id,w.phase,w.kind,w.created
              FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE i.request_id=? ORDER BY w.id""", (request_id,))
            result["inquiries"] = records(db, "SELECT id,proposed_by,created FROM hive_inquiries WHERE request_id=? ORDER BY id", (request_id,))
            result["memberships"] = records(db, """SELECT m.inquiry_id,m.drone_id,m.created,
              CASE WHEN m.commitment='initial_rotation' THEN 1 ELSE 0 END AS initial_rotation
              FROM hive_inquiry_members m JOIN hive_inquiries i ON i.id=m.inquiry_id WHERE i.request_id=? ORDER BY m.inquiry_id,m.drone_id""", (request_id,))
            result["actors"] = records(db, """SELECT d.id,d.created FROM hive_drones d JOIN hive_worker_queue q ON q.drone_id=d.id
              WHERE q.request_id=? ORDER BY d.id""", (request_id,))
            result["owned_drone_claims"] = records(db, "SELECT drone_id,owner,phase FROM hive_worker_queue WHERE request_id=? AND owner<>''", (request_id,))
            result["owned_queen_claims"] = records(db, "SELECT owner FROM hive_worker_queen WHERE request_id=? AND owner<>''", (request_id,))
            result["state_available"] = True
        finally:
            db.close()
        try:
            result["debug"] = read_debug(debug_path, request_id)
            if not result["debug"]["available"]:
                result["incomplete_reasons"].append(result["debug"]["incomplete_reason"])
        except (OSError, sqlite3.Error, ValueError) as exc:
            result["debug"] = {"available": False, "births": [], "fit_queries": [], "fit_calls": []}
            result["incomplete_reasons"].append("debug evidence incomplete: " + str(exc))
    except (OSError, sqlite3.Error, ValueError, KeyError, TypeError) as exc:
        result["incomplete_reasons"].append(str(exc))
    result["saved_work_count"] = len(result.get("work", []))
    result["max_work_id"] = result.get("work", [{}])[-1].get("id") if result.get("work") else None
    return result


def pairs(rows, kinds):
    return {(row["inquiry_id"], row["drone_id"]) for row in rows if row["kind"] in kinds}


def distribution(values):
    by_inquiry = collections.Counter(inquiry for inquiry, drone in values)
    by_drone = collections.Counter(drone for inquiry, drone in values)
    largest = max(by_inquiry.values(), default=0)
    return {"pairs": len(values), "unique_drones": len(by_drone), "by_inquiry": dict(sorted(by_inquiry.items())),
            "inquiries_with_two_or_more": sum(count >= 2 for count in by_inquiry.values()),
            "largest_inquiry_count": largest, "largest_share_of_unique_drones": largest / len(by_drone) if by_drone else None,
            "overlapping_drones": sum(count > 1 for count in by_drone.values())}


def point(run, count, cutoffs, latest=False):
    work = run["work"][:count]
    selected_ids = {row["id"] for row in work}
    boundary = work[-1]["created"] if work else None
    births = [row for row in run["debug"]["births"] if row["work_id"] in selected_ids]
    born = {row["drone_id"] for row in births}
    all_mapped = {row["drone_id"] for row in run["debug"]["births"]}
    forced = {(row["inquiry_id"], row["drone_id"]) for row in run["memberships"]
              if row["initial_rotation"] and (latest or row["drone_id"] in born)}
    explicit = pairs(work, {"PROPOSE", "JOIN"})
    memberships = forced | explicit
    observer = pairs(work, OBSERVER_KINDS) & memberships
    non_join = pairs(work, NON_JOIN_KINDS) & memberships
    repetitions = collections.Counter((row["inquiry_id"], row["drone_id"]) for row in work if row["kind"] in NON_JOIN_KINDS)
    previous_cut = max((cut for cut in cutoffs if cut < count), default=0)
    previous_start = max((cut for cut in cutoffs if cut < previous_cut), default=0)
    window = work[previous_cut:count]
    previous_window = work[previous_start:previous_cut]
    window_non_join = pairs(window, NON_JOIN_KINDS) & memberships
    retained = window_non_join & pairs(previous_window, NON_JOIN_KINDS)
    commitments = {drone for inquiry, drone in memberships}
    model_committed = {drone for inquiry, drone in explicit}
    child_work = collections.defaultdict(list)
    for row in work:
        if row["drone_id"] in born:
            child_work[row["drone_id"]].append(row)
    lifecycle = []
    for birth in births:
        drone = birth["drone_id"]
        rows = child_work[drone]
        def first(kinds):
            return next((row["id"] for row in rows if row["kind"] in kinds), None)
        lifecycle.append({**birth, "initial_automatic_inquiries": sorted(i for i, d in forced if d == drone),
            "model_commit_work_id": first({"PROPOSE", "JOIN"}), "first_non_join_work_id": first(NON_JOIN_KINDS),
            "first_contribute_work_id": first({"CONTRIBUTE"}), "saved_child_work_count": len(rows),
            "memberships": sorted(i for i, d in memberships if d == drone)})
    before_boundary = lambda row: boundary is not None and instant(row["created"] if "created" in row else row["ts"]) <= instant(boundary)
    known_actors = {row["id"] for row in run["actors"]}
    fit_calls = [row for row in run["debug"]["fit_calls"]
                 if (latest or before_boundary(row)) and row["drone_id"] in (known_actors if latest else born)]
    fit_queries = [row for row in run["debug"]["fit_queries"] if row["outcome"] == "ok"
                   and (latest or before_boundary(row)) and row["drone_id"] in (known_actors if latest else born)]
    spawns = [row["id"] for row in work if row["kind"] == "SPAWN"]
    mapped_work = {row["work_id"] for row in births}
    return {"saved_work_count": count, "through_work_id": work[-1]["id"] if work else None, "through_work_created": boundary,
        "automatic_memberships": distribution(forced), "model_commitment_pairs": distribution(explicit),
        "memberships": distribution(memberships), "observer_contributors_PJC": distribution(observer),
        "non_join_contributors_PC": distribution(non_join),
        "repeated_non_join_participants_PC_at_least_two_records": distribution({pair for pair, total in repetitions.items() if total >= 2} & memberships),
        "actions": dict(collections.Counter(row["kind"] for row in work)),
        "expand_actions": dict(collections.Counter(row["kind"] for row in work if row["phase"] == "expand")),
        "recent_window": {"after_saved_work_count": previous_cut, "through_saved_work_count": count,
            "saved_work_count": len(window), "actions": dict(collections.Counter(row["kind"] for row in window)),
            "observer_contributors_PJC": distribution(pairs(window, OBSERVER_KINDS) & memberships),
            "non_join_contributors_PC": distribution(window_non_join),
            "non_join_pairs_also_active_in_previous_window": len(retained) if previous_cut else None,
            "non_join_drones_also_active_in_previous_window": len({d for i, d in retained}) if previous_cut else None},
        "newborns": {"spawn_actions": len(spawns), "mapped_births": len(births), "unmapped_spawn_work_ids": sorted(set(spawns) - mapped_work),
            "automatically_assigned": len({d for i, d in forced} & born), "model_committed": len(model_committed & born),
            "with_non_join_PC_work": len({d for i, d in non_join} & born),
            "with_CONTRIBUTE": len({row["drone_id"] for row in work if row["kind"] == "CONTRIBUTE"} & born),
            "still_uncommitted": len(born - commitments), "lifecycle": lifecycle},
        "fit_exposure": {"successful_context_queries": len(fit_queries), "model_calls_started": len(fit_calls),
            "boundary": "latest diagnostic snapshot for known saved actors" if latest else "through final prefix work timestamp for mapped newborns",
            "distinct_drones": len({row["drone_id"] for row in fit_calls}),
            "calls_by_mode": dict(collections.Counter(row["newborn_mode"] or "unspecified" for row in fit_calls)),
            "query_log_ids": [row["id"] for row in fit_queries], "call_log_ids": [row["log_id"] for row in fit_calls]},
        "automatic_memberships_without_birth_mapping": [row for row in run["memberships"] if row["initial_rotation"] and row["drone_id"] not in all_mapped]}


def brief(run, snapshot):
    value = {"test": run["test"], "label": run["label"], "newborn_mode": run.get("newborn_mode"),
             "status": run.get("request", {}).get("status"), "saved_work_count": run["saved_work_count"],
             "owned_claims": len(run.get("owned_drone_claims", [])) + len(run.get("owned_queen_claims", [])),
             "incomplete_reasons": run["incomplete_reasons"]}
    if snapshot:
        value.update(matched_saved_work_count=snapshot["saved_work_count"],
            teams_with_two_observer_contributors=snapshot["observer_contributors_PJC"]["inquiries_with_two_or_more"],
            teams_with_two_non_join_contributors=snapshot["non_join_contributors_PC"]["inquiries_with_two_or_more"],
            largest_contributor_share=snapshot["observer_contributors_PJC"]["largest_share_of_unique_drones"],
            largest_non_join_contributor_share=snapshot["non_join_contributors_PC"]["largest_share_of_unique_drones"],
            overlapping_members=snapshot["memberships"]["overlapping_drones"],
            newborns=snapshot["newborns"]["mapped_births"], newborns_contributed=snapshot["newborns"]["with_CONTRIBUTE"],
            fit_calls=snapshot["fit_exposure"]["model_calls_started"])
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--cutoffs", type=int, nargs="+", default=[200, 400])
    args = parser.parse_args()
    if args.out.exists():
        raise FileExistsError("Refusing to overwrite " + str(args.out))
    if any(count < 1 for count in args.cutoffs):
        raise ValueError("analysis cutoffs must be positive saved-work counts")
    manifest = load_json(args.manifest)
    if not isinstance(manifest.get("runs"), list) or not manifest["runs"]:
        raise ValueError("manifest requires a nonempty runs list")
    cutoffs = sorted(set(args.cutoffs))
    runs = [inspect(entry) for entry in manifest["runs"]]
    common = min(run["saved_work_count"] if run["state_available"] else 0 for run in runs)
    matched = sorted({cut for cut in cutoffs if cut <= common} | ({common} if common else set()))
    for run in runs:
        run["prefixes"] = []
        run["latest"] = None
        if run["state_available"]:
            counts = sorted({cut for cut in cutoffs if cut <= run["saved_work_count"]} | set(matched))
            run["prefixes"] = [point(run, count, cutoffs) for count in counts]
            run["latest"] = point(run, run["saved_work_count"], cutoffs, latest=True)
    comparisons = [{"saved_work_count": count, "runs": [brief(run, next(item for item in run["prefixes"] if item["saved_work_count"] == count)) for run in runs]} for count in matched]
    result = {"observed_utc": now(), "manifest_path": str(args.manifest.resolve()), "manifest": manifest,
        "requested_cutoffs": cutoffs, "current_common_saved_work_count": common, "matched_prefixes": comparisons,
        "method": {"prefix": "First N request-scoped saved work rows ordered by ID, not ID<=N, model calls, elapsed time, or an internal quota. PASS and Queen decisions have no work rows; convergence rows count if present.",
            "membership": "Model PROPOSE/JOIN pairs union explicit initial_rotation placements. Machine assignments never count as model commitments or contributions. Overlap persists.",
            "contributors": "PJC matches the runtime observer: PROPOSE/JOIN/CONTRIBUTE. PC excludes JOIN-only authors; repeated PC means at least two such records, not a quality score. Recent windows lie between requested cutoffs.",
            "share": "Largest inquiry's unique participants divided by unique participants across inquiries under the named measure; overlap means per-inquiry shares need not sum to one.",
            "newborns": "Exact spawned_drone_id linked to committed SPAWN work_id in diagnostics. Missing birth mappings stay explicit. No seed-count or ID heuristic.",
            "fit": "Successful named context SQL reads and actual llm.start user payload needs_fit_choice=true are separate measures. Prefix exposure is time-bounded at its final work record, and newborn-scoped for historical prefixes.",
            "limits": "Read-only per-database snapshots, not one simultaneous five-run transaction. Distribution does not establish intellectual influence, novelty, answer quality, or a winning method. No model grading or automatic stopping. Missing state is incomplete, not zero performance."},
        "latest_summary": [brief(run, run["latest"]) for run in runs], "runs": runs}
    with args.out.open("x", encoding="utf-8", newline="\n") as output:
        json.dump(result, output, ensure_ascii=False, indent=2)
        output.write("\n")
    print(json.dumps({"artifact": str(args.out.resolve()), "current_common_saved_work_count": common,
        "common_comparison": comparisons[-1] if comparisons else None, "latest": result["latest_summary"]}, ensure_ascii=False))


if __name__ == "__main__":
    main()
