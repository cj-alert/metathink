"""Generate a new human report from saved resource-health evidence; never alter originals."""
import argparse
import collections
import json
import sys
from pathlib import Path


def load(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def cell(value):
    return str(value if value is not None else "—").replace("|", "\\|").replace("\n", " ")


def link(label, path):
    return f"[{label}](<{Path(path).resolve().as_posix()}>)"


def ids(values):
    return ", ".join(str(value) for value in values) or "none"


def work_label(work):
    return f"{work['kind']} {work['id']} → inquiry {work['inquiry_id']}" if work else "not observed"


def table(lines, headings, rows):
    lines.extend(["| " + " | ".join(headings) + " |", "| " + " | ".join("---" for _ in headings) + " |"])
    lines.extend("| " + " | ".join(cell(value) for value in row) + " |" for row in rows)
    lines.append("")


def report(data, observation, evidence_path, observation_path):
    summary, state, debug = data["summary"], data["state"], data["debug"]
    decisions = [row for row in debug["resource_decisions"] if row["outcome"] == "saved"]
    calls = {row["start_log_id"]: row for row in debug["resource_calls"]}
    masked = [row for row in data["newborn_choices"] if row["first_menu"] and row["first_menu"]["omitted_inquiry_ids"]]
    ordinary = [row for row in data["newborn_choices"] if row["first_menu_after_review_log_id"] and not row["omit_oversized"]]
    verified = [row for row in masked if (row["first_menu"].get("matched_model_call") or {}).get("matches_logged_menu")
                and row["first_menu"]["matched_model_call"].get("logged_omitted_ids_absent")
                and row["first_menu"]["matched_model_call"].get("outcome") == "answered"]
    joined_alternatives = sum(row["first_join"] is not None and not row["join_to_first_menu_omitted"] for row in verified)
    later_contributed = sum(row["first_contribute"] is not None for row in verified)
    lines = [f"# {Path(data['run_folder']).name} — resource health", "",
        f"Saved evidence at {data['analyzed_utc']}. Request status: **{summary['request_status']}**; finish reason: `{summary['finish_reason']}`.", "",
        f"The run recorded **{len(decisions)} accepted Queen resource reviews**. Actual answered model requests confirm omitted inquiries were absent from the first menus of **{len(verified)} newborns**; **{joined_alternatives}** subsequently joined alternatives and **{later_contributed}** later saved a `CONTRIBUTE` action.", "",
        "This exercises the visibility mechanism in a live hive. It does not establish that omission caused better distribution, deeper thinking, better hypotheses, or scientific novelty.", "",
        "## Evidence and observation", "",
        f"- {link('Original analysis JSON', evidence_path)}", f"- {link('Saved observation plan', observation_path)}"]
    if observation.get("dashboard_url"):
        lines.append(f"- [Run dashboard](<{observation['dashboard_url']}>)")
    lines.extend(["", f"Observation objective: {observation.get('objective', 'not recorded')}", ""])
    cadence = observation.get("cadence_change")
    if cadence:
        lines.extend([f"The observation record changed review cadence from {observation.get('initial_review_every_births')} to {observation.get('review_every_births')} successful births at {cadence.get('at_successful_births')} births. Recorded reason: {cadence.get('reason')}", ""])
    if observation.get("status") and observation["status"] != summary["request_status"]:
        lines.extend([f"The observation plan retains its `{observation['status']}` label; the later database evidence above supplies the run status.", ""])
    lines.extend(["## Queen resource decisions", "", "These are the Queen's local action scores, not parent-hypothesis grades. The recorded reasons below are verbatim.", ""])
    table(lines, ["Birth snapshot", "Action / selected inquiries", "Relevance", "Inquiry value", "Decision log", "Call start / finish"], [
        [row["successful_births"], f"{row['action']['kind']} {ids(row['action']['inquiry_ids'])}", row["action"]["relevance"], row["action"]["inquiry_value"], row["log_id"],
         f"{row['call_start_log_id']} / {calls.get(row['call_start_log_id'], {}).get('finish_log_id', 'not recorded')}"] for row in decisions])
    for row in decisions:
        lines.extend([f"### Reason at {row['successful_births']} births", ""])
        lines.extend("> " + line for line in row["action"]["body"].split("\n"))
        lines.append("")
    current = state.get("current_resource_selection")
    lines.extend([f"Current saved selection: **{ids(current['selected_ids']) if current else 'no resource review saved'}**. Historical decisions are listed separately above.", "",
                  "## Participation at the first review and final snapshot", ""])
    first_call = calls.get(decisions[0]["call_start_log_id"]) if decisions else None
    if first_call:
        snapshot = first_call["resource_input"]
        earliest = {row["id"]: row for row in snapshot["inquiries"]}
        lines.extend([f"First review: {snapshot['successful_births']} successful births plus {state['seed_count']} initial seeds = {snapshot['successful_births'] + state['seed_count']} drones. Final snapshot: {state['population']} drones ({state['successful_spawn_count']} births).", "",
            "Committed membership and contribution are different measures. The first-review contributor count includes `JOIN`; the final non-JOIN column counts distinct committed authors of `PROPOSE` or `CONTRIBUTE`. Do not subtract those unlike columns to infer improvement.", ""])
        table(lines, ["Inquiry", "First-review members", "First-review JOIN-inclusive contributors", "Final members", "Final non-JOIN contributors"], [
            [row["id"], earliest.get(row["id"], {}).get("committed_member_count"), earliest.get(row["id"], {}).get("contributing_member_count"),
             len(row["committed_member_ids"]), len(row["non_join_contributor_ids"])]
            for row in state["inquiries"] if row["id"] != snapshot["root_inquiry_id"]])
    else:
        lines.extend(["No accepted first-review input is available for comparison.", ""])
    lines.extend([f"Final evidence has {summary['non_join_contributing_pair_count']} inquiry–drone non-JOIN contributing pairs across {summary['non_join_working_inquiries']} inquiries with at least two such contributors. Overlapping membership means pairs are not unique drones.", "",
                  "## Masked first menus and subsequent actions", "", "Call IDs below refer to diagnostic log rows; action IDs refer to saved operational work. A repeated READ/menu is not counted as another newborn.", ""])
    rows = []
    for row in masked:
        menu, call = row["first_menu"], row["first_menu"].get("matched_model_call") or {}
        accepted = call.get("accepted_action")
        action = f"{accepted['kind']} work {accepted['work_id']}" if accepted else "not recorded"
        rows.append([row["drone_id"], ids(menu["omitted_inquiry_ids"]), ids(call.get("actual_signal_ids") or []),
            f"{call.get('start_log_id', '—')} / {call.get('finish_log_id', '—')}",
            f"{call.get('outcome', 'not matched')}; exact menu={call.get('matches_logged_menu', False)}",
            action, work_label(row["first_join"]), work_label(row["first_contribute"])])
    table(lines, ["Drone", "Omitted", "Actual request cards", "Start / finish", "Call evidence", "First accepted action", "Later first JOIN", "Later first CONTRIBUTE"], rows)
    choices = collections.Counter(row["first_join"]["inquiry_id"] for row in ordinary if row["first_join"])
    lines.extend([f"After a recorded policy review, {len(ordinary)} ordinary-cohort newborns received first menus without cohort masking; {sum(row['first_join'] is not None for row in ordinary)} joined and {sum(row['first_contribute'] is not None for row in ordinary)} later saved `CONTRIBUTE`. Their first JOIN destinations: {', '.join(f'inquiry {key}: {value}' for key, value in sorted(choices.items())) or 'none observed'}.", ""])
    probability = data["configured_newborn_omit_probability"]
    probability_text = "1/3" if probability == 1 / 3 else str(probability)
    not_served = state["successful_spawn_count"] - summary["newborns_with_first_menu"]
    lines.extend([f"The omission cohort contains {summary['cohort_counts']['omit_selected']}/{state['successful_spawn_count']} newborns. Its configured probability is {probability_text}, an independent birth draw, **not a quota**; the observed assignment proportion can differ in a finite sample. Cohort membership alone does not mean an inquiry was actually omitted. {not_served} born drones had not yet received a first menu in this snapshot; this pending count describes exposure, not the assignment proportion.", "",
                  "## Calls, errors, and stopped state", ""])
    outcomes = ", ".join(f"{key}: {value}" for key, value in sorted(debug["finish_outcomes"].items())) or "none"
    statuses = ", ".join(f"HTTP {key}: {value}" for key, value in sorted(summary["http_status_counts"].items())) or "none"
    lines.extend([f"- Calls: {summary['calls_started']} started, {summary['calls_finished']} finished; outcomes: {outcomes}; {statuses}.",
        f"- Resource calls: {summary['resource_call_starts']} started, {summary['resource_answered_calls']} answered; {summary['accepted_resource_reviews']} decisions accepted.",
        f"- Actual masked requests: {summary['actual_masked_calls_answered']} answered across {summary['actual_masked_answered_drone_count']} distinct drones; {summary['menu_model_mismatch_count']} menu/payload mismatches.",
        f"- Recorded error events: {json.dumps(summary['error_event_counts'], ensure_ascii=False)}. Multiple events can describe one failure.",
        f"- Saved state: `{summary['request_status']}` / `{summary['finish_reason']}`; owned drone claims={summary['owned_drone_claims']}; Queen owned={summary['queen_owned']}; in-flight calls={summary['in_flight_calls']}.", ""])
    unexercised = []
    if not data["discover_revelations"]:
        unexercised.append("explicit DISCOVER revealing a previously omitted inquiry")
    if not any(row["action"]["kind"] == "CLEAR" for row in decisions):
        unexercised.append("Queen CLEAR")
    if not any(len(row["action"]["inquiry_ids"]) > 1 for row in decisions):
        unexercised.append("selection of multiple inquiries")
    lines.extend(["## Limits", "", "Not observed in this live evidence: " + ("; ".join(unexercised) or "none of the tracked optional paths") + ".", ""])
    lines.extend("- " + value for value in data["limits"])
    lines.extend(["- Menu omission does not isolate later information: READ and CONNECT remain allowed. Changed choices cannot be attributed solely to omission, and these observations do not establish reciprocal intellectual influence.",
                  "- This report derives from saved evidence only; it does not inspect OS process state or validate the scientific content of generated hypotheses.", ""])
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", required=True, type=Path)
    parser.add_argument("--observation", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    if args.out.exists():
        raise FileExistsError(f"refusing to overwrite {args.out}")
    text = report(load(args.evidence), load(args.observation), args.evidence, args.observation)
    with args.out.open("x", encoding="utf-8", newline="\n") as stream:
        stream.write(text)
    print(json.dumps({"report": str(args.out.resolve()), "evidence": str(args.evidence.resolve())}))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(json.dumps({"report_error": str(error)}, ensure_ascii=False), file=sys.stderr)
        sys.exit(1)
