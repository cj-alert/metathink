"""Print the saved queen prompt templates and draft criteria; no model or database access."""

import json
import sys
from pathlib import Path


def section(title, text):
    print(f"\n## {title}\n\n```text\n{text}\n```")


def criteria(title, items):
    print(f"\n## {title}\n")
    print("| Criterion | Draft score endpoints | Weight | Minimum score |")
    print("| --- | --- | ---: | --- |")
    for item in items:
        levels = "; ".join(f"{i}: {level['label']} — {level.get('description', '')}" for i, level in enumerate(item["levels"]))
        cells = [item["name"], levels, str(item.get("weight", "unspecified")),
                 str(item.get("min_score", "none"))]
        print("| " + " | ".join(value.replace("|", "\\|").replace("\n", " ") for value in cells) + " |")
    for item in items:
        section(item["name"] + " — exact criterion", item["description"])


def main():
    path = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).with_name("config.json")
    config = json.loads(path.read_text(encoding="utf-8-sig"))
    print(f"# MetaThought v{config['version']} — queen prompts and scoring")
    print(f"\nSource: `{path.resolve()}`")
    print("\nThese are verbatim saved templates and rubric descriptions. Actual calls also contain "
          "the relevant runtime evidence; this is not a historical rendered request.")
    print(f"\nQueen model: `{config['models']['queen']}`.")
    print("\n## Contract and assessment ownership\n")
    print("Both levels execute a reviewed, explicitly confirmed contract. Configuration criteria "
          "seed drafting only; the saved confirmed text supplies execution criteria. Committed "
          "drones provide actual 0–1 scores with evidence and uncertainty for candidates or methods. "
          "Queens guide and judge sufficiency from those separate profiles, without replacing scores "
          "or applying a numerical collapse formula. The values below are draft endpoints and targets, "
          "not actual result scores. Auxiliary prompt scoring remains incomplete.")
    print("\nMeta evidence includes the distinct task and meta contracts, operational trajectories "
          "and attributed method assessments. Diagnostic logs are excluded. The fixed collapse_format "
          "is appended to the mutable collapse instruction and selects actual saved candidate IDs.")
    section("Meta-queen objective", config["meta_queen"]["objective"])
    section("Meta-queen runtime context", config["meta_queen"]["context"])
    criteria("Meta contract draft criteria", config["meta_queen"]["criteria"])
    for key in ("mq_action", "mq_return", "mq_parent", "mq_donor",
                "mq_target", "mq_mutate", "mq_select", "mq_propose", "mq_compare"):
        section(key, config["prompts"][key])
    for key in ("objective", "intent", "context"):
        section("Base task " + key, config["experiment"][key])
    criteria("Base contract draft criteria", config["experiment"]["criteria"])
    for key in ("contract", "assess", "collapse", "collapse_format", "guide"):
        section("Base queen " + key, config["prompts"][key])
    for key in ("system", "input_prefix", "choice", "score"):
        section("Decisions " + key, config["decisions"][key])


if __name__ == "__main__":
    main()
