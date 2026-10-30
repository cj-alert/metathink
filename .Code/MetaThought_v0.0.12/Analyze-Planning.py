"""Read planning diagnostics without creating or opening hive state; no model calls."""
import importlib.util
import json
import sys
from pathlib import Path


def main():
    if len(sys.argv) != 4:
        raise SystemExit('Usage: Analyze-Planning.py RUN_FOLDER RATES_JSON OUTPUT_PREFIX')
    run, rate_path, prefix = map(Path, sys.argv[1:])
    outputs = [Path(str(prefix) + ext) for ext in ('.json', '.md')]
    if any(p.exists() for p in outputs):
        raise FileExistsError('Preserve previous report; choose a new output prefix')
    spec = importlib.util.spec_from_file_location('planning_receipts', Path(__file__).with_name('Analyze-Evolution.py'))
    receipts = importlib.util.module_from_spec(spec)
    sys.dont_write_bytecode = True
    spec.loader.exec_module(receipts)
    config = json.loads((run / 'config.json').read_text(encoding='utf-8-sig'))
    rates = json.loads(rate_path.read_text(encoding='utf-8-sig'))
    calls, aggregates, anomalies, events = receipts.debug_receipts(run / config['files']['debug'], rates)
    total = receipts.totals(calls).get('all', {})
    result = {'run_folder': str(run.resolve()), 'pricing': rates, 'calls': calls,
              'totals': total, 'rejections': receipts.rejection_summary(events),
              'failure_events': events, 'association_anomalies': anomalies,
              'event_and_sql_aggregates': aggregates,
              'operational_state_exists': (run / config['files']['state']).exists(),
              'cost_basis': 'saved published-rate estimate; not returned billed dollars'}
    with outputs[0].open('x', encoding='utf-8') as f:
        json.dump(result, f, ensure_ascii=False, indent=2)
    lines = ['# Queen planning receipt', '',
             'Developer-only read-only extraction. No model calls or operational hive state opened.', '',
             f"Operational state exists: {result['operational_state_exists']}",
             f"Provider calls: {len(calls)}", f"Usage and estimated cost: {json.dumps(total)}", '',
             '## Exact exchanges', '']
    for c in calls:
        lines += [f"### {c['call_id']}", '', f"Model: {c['model']}; outcome: {c.get('outcome')}; HTTP: {c.get('http_status')}", '']
        for m in receipts.object_value(c.get('request')).get('messages') or []:
            lines += [f"#### {m['role']}", '', str(m.get('content')), '']
        lines += ['#### Reply', '', c.get('reply') or c.get('response_raw') or str(c.get('error')), '']
    with outputs[1].open('x', encoding='utf-8') as f:
        f.write('\n'.join(lines))
    print(json.dumps({'calls': len(calls), 'totals': total, 'rejections': result['rejections'],
                      'operational_state_exists': result['operational_state_exists']}, indent=2))


if __name__ == '__main__':
    main()
