"""Summarize completed Run-WorkerTests receipts without changing test evidence."""
import argparse
import json
from pathlib import Path


def summarize(receipt_path):
    receipt = json.loads(receipt_path.read_text(encoding="utf-8-sig"))
    raw = Path(receipt["stdout"]).read_text(encoding="utf-8-sig")
    if raw and not raw.endswith("\n"):
        raise ValueError(f"Incomplete test log: {receipt['stdout']}")
    rows = [json.loads(line) for line in raw.splitlines() if line.strip()]
    passed = [r["Test"] for r in rows if r.get("Action") == "pass" and r.get("Test")]
    failed = [r["Test"] for r in rows if r.get("Action") == "fail" and r.get("Test")]
    output = "".join(r.get("Output", "") for r in rows)
    terminals = [r for r in rows if r.get("Action") in ("pass", "fail") and not r.get("Test")]
    network_lines = [line.strip() for line in output.splitlines() if line.startswith("focused_local_network_attempts=")]
    network = int(network_lines[-1].split("=", 1)[1]) if network_lines else None
    return {
        "receipt": str(receipt_path),
        "exit_code": receipt["exit_code"],
        "elapsed_seconds": receipt["elapsed_seconds"],
        "top_level_passed": sum("/" not in name for name in passed),
        "subtests_passed": sum("/" in name for name in passed),
        "failed_tests": failed,
        "network_attempts": network,
        "package_outcome": terminals[-1]["Action"] if terminals else "incomplete",
        "build_errors": [r["Output"] for r in rows if r.get("Action") == "build-output"],
        "passed": receipt["exit_code"] == 0 and bool(terminals) and terminals[-1]["Action"] == "pass" and not failed and network == 0,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--folder", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    receipts = sorted(args.folder.glob("*.receipt.json"))
    if not receipts:
        raise ValueError("No completed local-test receipts found")
    attempts = [summarize(path) for path in receipts]
    result = {"attempts": attempts, "latest": attempts[-1]}
    with args.out.open("x", encoding="utf-8") as target:
        json.dump(result, target, indent=2)
        target.write("\n")
    print(json.dumps(result["latest"]))


if __name__ == "__main__":
    main()
