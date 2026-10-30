"""Print the saved queen prompt templates and rubrics; no model or database access."""

import json
import sys
from pathlib import Path


def section(title, text):
    print(f"\n## {title}\n\n```text\n{text}\n```")


def criteria(title, items):
    print(f"\n## {title}\n")
    print("| Criterion | Levels (zero-based) | Weight | Minimum score |")
    print("| --- | --- | ---: | --- |")
    for item in items:
        levels = "; ".join(f"{i}: {level['label']}" for i, level in enumerate(item["levels"]))
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
    print("\n## How the two evaluations differ\n")
    print("The meta-queen evaluates the thinking method with plain-text 0/1/2 ratings. "
          "There is no parsed aggregate score or numeric RETURN threshold. Weights are supplied "
          "as rubric metadata; the controller does not calculate a weighted total.")
    print("\nThe base queen uses Decisions for each task criterion. The score is "
          "sum(zero_based_level * reported_probability). With three levels, this is "
          "p(level 1) + 2*p(level 2). Every configured minimum must be met to set criteria_met. "
          "Weights are not combined into an aggregate acceptance score. The queen separately "
          "chooses RETURN or CONTINUE and can return unmet criteria honestly. "
          "Choice probabilities and confidence are separate self-reported outputs, not rubric scores.")
    print("\nThe GA controller receives meta_queen intent and a separate task_contract, method "
          "instructions, trial identities, attributed operational evidence and persisted assessments. "
          "It assesses the imported reference before its first action. Reference characterization "
          "is not comparative improvement. Debug logs and diagnostic run usage are excluded.")
    section("Meta-queen objective", config["meta_queen"]["objective"])
    section("Meta-queen runtime context", config["meta_queen"]["context"])
    criteria("Meta-queen method rubric", config["meta_queen"]["criteria"])
    for key in ("mq_action", "mq_assess", "mq_return", "mq_parent", "mq_donor",
                "mq_target", "mq_mutate", "mq_select", "mq_propose", "mq_compare"):
        section(key, config["prompts"][key])
    for key in ("objective", "intent", "context"):
        section("Base task " + key, config["experiment"][key])
    criteria("Base queen task rubric", config["experiment"]["criteria"])
    for key in ("contract", "collapse", "guide"):
        section("Base queen " + key, config["prompts"][key])
    for key in ("system", "input_prefix", "choice", "score"):
        section("Decisions " + key, config["decisions"][key])


if __name__ == "__main__":
    main()
