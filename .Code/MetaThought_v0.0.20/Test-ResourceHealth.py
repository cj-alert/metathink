"""Offline stdlib fixtures for cohort evidence; no hive, provider, or saved run is changed."""
import importlib.util
import contextlib
import json
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
SOURCE = Path(__file__).resolve().parent


def load_script(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), SOURCE / name)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


analysis = load_script("Analyze-ResourceHealth.py")
reporting = load_script("Report-ResourceHealth.py")


def work(number, inquiry, drone, kind):
    return {"id": number, "inquiry_id": inquiry, "target_inquiry_id": None, "drone_id": drone,
            "phase": "contribute", "kind": kind, "created": f"2026-10-29T00:00:{number:02d}Z", "body_excerpt": "SYNTHETIC"}


def snapshot():
    actors = [{"drone_id": drone, "omit_oversized": mode, "focus_id": focus, "owner": "", "phase": "contribute",
               "created": "2026-10-29T00:00:00Z"}
              for drone, mode, focus in [(1, None, 2), (2, 0, 2), (3, 1, 3), (4, 2, 3), (5, 2, 3), (6, 2, 1)]]
    works = [work(1, 2, 1, "PROPOSE"), work(2, 2, 1, "SPAWN"), work(3, 2, 1, "SPAWN"),
             work(4, 2, 1, "SPAWN"), work(5, 2, 1, "SPAWN"), work(6, 1, 1, "SPAWN"),
             work(7, 2, 2, "JOIN"), work(8, 2, 2, "CONTRIBUTE"), work(9, 3, 3, "JOIN"),
             work(10, 3, 4, "CONTRIBUTE"), work(11, 3, 4, "CONTRIBUTE"), work(12, 3, 4, "RESEARCH")]
    inquiries = [{"id": 1, "committed_member_ids": [], "non_join_contributor_ids": [], "join_inclusive_contributor_ids": []},
                 {"id": 2, "committed_member_ids": [1, 2], "non_join_contributor_ids": [1, 2], "join_inclusive_contributor_ids": [1, 2]},
                 {"id": 3, "committed_member_ids": [3, 4, 5], "non_join_contributor_ids": [4], "join_inclusive_contributor_ids": [3, 4]}]
    state = {"snapshot_utc": "2026-10-29T00:00:20Z", "request": {"id": 1, "status": "paused", "finish_reason": "observation_paused"},
             "population": 6, "seed_count": 1, "successful_spawn_count": 5,
             "non_join_working_inquiry_count": 1, "non_join_contributing_pairs": [
                 {"inquiry_id": 2, "drone_id": 1}, {"inquiry_id": 2, "drone_id": 2}, {"inquiry_id": 3, "drone_id": 4}],
             "newborn_cohorts": {"ordinary": 1, "omit_selected": 1, "focused": 3},
             "inquiries": inquiries, "owned_drone_claims": [], "current_queen": {"owner": ""}, "current_resource_selection": None}
    return state, actors, works


def debug_fixture(path):
    db = sqlite3.connect(path)
    db.execute("CREATE TABLE hive_log(id INTEGER PRIMARY KEY,ts TEXT,event TEXT,scope TEXT,drone_id INTEGER,model TEXT,outcome TEXT,details TEXT,request_id INTEGER)")
    sequence = 0

    def event(name, drone, fields, scope="drone"):
        nonlocal sequence
        sequence += 1
        db.execute("INSERT INTO hive_log VALUES(?,?,?,?,?,?,?,?,1)",
                   (sequence, f"2026-10-29T00:00:{sequence:02d}Z", name, scope, drone, "synthetic", fields.get("outcome"), json.dumps(fields)))

    def birth(drone, mode, work_id, unavailable=False):
        fields = {"worker": "parent", "phase": "expand", "work_id": work_id, "spawned_drone_id": drone,
                  "action": {"kind": "SPAWN", "inquiry_id": 2}, "omit_oversized": mode == 1}
        if mode == 2:
            fields.update(newborn_cohort=2, newborn_mode="focus", invited_inquiry_id=2,
                          assigned_inquiry_id=None if unavailable else 3, focus_basis="members",
                          population_before_assignment=None if unavailable else 1,
                          assigned_member_count_before=None if unavailable else 1,
                          assigned_contributor_count_before=None if unavailable else 0,
                          focus_applied=not unavailable, eligible=not unavailable,
                          focus_unavailable="no_research_inquiries" if unavailable else None)
        event("worker_action_saved", 1, fields, "hive")

    birth(2, 0, 2)
    birth(3, 1, 3)
    birth(4, 2, 4)
    birth(5, 2, 5)
    birth(6, 2, 6, True)
    for drone, mode, actual in [(2, 0, [2, 3]), (3, 1, [3]), (4, 2, [2, 3])]:
        fields = {"worker": str(drone), "phase": "contribute", "omit_oversized": mode == 1,
                  "discovered": False, "shown_inquiry_ids": actual, "omitted_inquiry_ids": [2] if mode == 1 else []}
        value = {"drone": {"id": drone}, "phase": "contribute"}
        if mode == 2:
            for key in ("omit_oversized", "discovered", "omitted_inquiry_ids"):
                del fields[key]
            fields.update(newborn_cohort=2, newborn_mode="focus", assigned_inquiry_id=3,
                          background_inquiry_ids=actual, choice_inquiry_ids=[3], initial_focus_pending=True)
            value.update(initial_focus_pending=True, assigned_inquiry={"id": 3}, hive_background=[{"id": item} for item in actual])
        else:
            value.update(needs_fit_choice=True, inquiry_signals=[{"id": item} for item in actual])
        event("newborn_menu", drone, fields, "hive")
        event("llm.start", drone, {"worker": str(drone), "phase": "contribute", "request": json.dumps({"messages": [{"role": "user", "content": json.dumps(value)}]})})
        event("llm.response", drone, {"worker": str(drone), "phase": "contribute", "status_code": 200})
        event("llm.finish", drone, {"worker": str(drone), "phase": "contribute", "outcome": "answered"})
        event("worker_action_saved", drone, {"worker": str(drone), "phase": "contribute", "work_id": {2: 7, 3: 9, 4: 10}[drone],
                                            "action": {"kind": "CONTRIBUTE" if mode == 2 else "JOIN", "inquiry_id": 3 if mode else 2}}, "hive")
    db.commit()
    db.close()


class ResourceHealthFixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="metathought-resource-fixture-")
        self.addCleanup(self.temp.cleanup)
        self.debug_path = Path(self.temp.name) / "debug.sqlite"
        debug_fixture(self.debug_path)
        self.state, self.actors, self.work = snapshot()
        self.debug = analysis.read_debug(self.debug_path, 1)

    def result(self, baseline=None):
        result = analysis.combine(self.state, self.actors, self.work, self.debug, baseline)
        result.update(run_folder="SYNTHETIC cohort fixture", analyzed_utc="2026-10-29T00:00:30Z",
                      configured_newborn_omit_probability=1 / 3, configured_newborn_focus_probability=1 / 3)
        return result

    def test_legacy_modes_and_forced_background_are_distinct(self):
        result = self.result()
        self.assertEqual(result["summary"]["menu_model_mismatch_count"], 0)
        self.assertEqual(result["summary"]["actual_masked_model_calls"], 1)
        force = next(row for row in result["newborn_choices"] if row["drone_id"] == 4)
        self.assertEqual(force["newborn_cohort"], 2)
        self.assertFalse(force["omit_oversized"])
        self.assertEqual(force["first_menu"]["matched_model_call"]["actual_background_inquiry_ids"], [2, 3])
        self.assertEqual(force["first_menu"]["matched_model_call"]["actual_choice_inquiry_ids"], [3])

    def test_join_only_pending_and_unapplied_are_not_contributions(self):
        result = self.result()
        cohorts = result["cohort_outcomes"]
        self.assertEqual(cohorts["queen_omission"]["non_join_contribution"], {"numerator": 0, "denominator": 1, "rate": 0})
        focus = cohorts["focused"]
        self.assertEqual(focus["activation"], {"numerator": 1, "denominator": 3, "rate": 1 / 3})
        self.assertEqual(focus["pending_first_model_call"], 2)
        self.assertEqual(focus["focus_unapplied"], 1)
        self.assertEqual(focus["placement_evidence_missing"], 0)
        self.assertEqual(focus["assigned_first_contribute"], {"numerator": 1, "denominator": 1, "rate": 1})
        self.assertEqual(focus["pending_assigned_first_contribute"], 1)
        self.assertEqual(focus["later_non_join_work"]["numerator"], 1)
        self.assertEqual(analysis.rate(0, 0)["rate"], None)

    def test_mismatched_forced_assignment_does_not_pass_context_check(self):
        with contextlib.closing(sqlite3.connect(self.debug_path)) as db:
            row = db.execute("SELECT id,details FROM hive_log WHERE event='llm.start' AND drone_id=4").fetchone()
            fields = json.loads(row[1])
            payload = json.loads(fields["request"])
            value = json.loads(payload["messages"][0]["content"])
            value["assigned_inquiry"]["id"] = 2
            payload["messages"][0]["content"] = json.dumps(value)
            fields["request"] = json.dumps(payload)
            db.execute("UPDATE hive_log SET details=? WHERE id=?", (json.dumps(fields), row[0]))
            db.commit()
        debug = analysis.read_debug(self.debug_path, 1)
        self.assertFalse(debug["newborn_menus"][-1]["matched_model_call"]["matches_logged_menu"])

    def test_baseline_excludes_old_births_and_keeps_support_counts_distinct(self):
        baseline = {"state": {"snapshot_utc": "2026-10-29T00:00:00Z", "successful_spawn_count": 2,
                              "inquiries": [{"id": 2, "committed_member_ids": [1, 2], "non_join_contributor_ids": [1]},
                                            {"id": 3, "committed_member_ids": [3], "non_join_contributor_ids": []}]},
                    "newborn_choices": [{"drone_id": 2}, {"drone_id": 3}]}
        result = self.result(baseline)
        window = result["observation"]
        self.assertEqual(window["cohort_outcomes"]["open_fit"]["births"], 0)
        self.assertEqual(window["cohort_outcomes"]["focused"]["births"], 3)
        self.assertEqual(window["least_supported_at_baseline"], [3])
        self.assertEqual(window["least_supported_gained_contributors"]["rate"], 1)
        focused = next(row for row in result["newborn_choices"] if row["drone_id"] == 4)
        self.assertEqual(focused["assigned_non_join_contributor_growth"]["before"], 0)
        self.assertEqual(focused["assigned_non_join_contributor_growth"]["after"], 1)
        rendered = reporting.report(result, {"objective": "SYNTHETIC"}, "evidence.json", "observation.json")
        self.assertIn("Newborn outcomes since the baseline", rendered)
        self.assertIn("policy compliance", rendered)
        self.assertIn("no eligible research inquiry", rendered)

    def test_legacy_report_without_new_rating_fields_remains_readable(self):
        result = self.result()
        del result["cohort_outcomes"]
        del result["observation"]
        result["newborn_choices"] = [row for row in result["newborn_choices"] if row["newborn_cohort"] != 2]
        for row in result["newborn_choices"]:
            del row["newborn_cohort"]
        rendered = reporting.report(result, {}, "old-evidence.json", "old-observation.json")
        self.assertIn("omission cohort", rendered)


if __name__ == "__main__":
    unittest.main(verbosity=2)
