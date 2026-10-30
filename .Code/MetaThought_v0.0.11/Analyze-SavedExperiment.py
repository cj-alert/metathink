"""Read saved hive evidence without opening the application or making model calls.

Usage: python Analyze-SavedExperiment.py RUN_FOLDER RATES_JSON OUTPUT_PREFIX
SQLite connections are read-only. Output files must be new. Costs use returned
input/output/cache usage; reasoning tokens are a subset of output, not added.
This developer analysis utility never supplies diagnostics to the hive.
It shares receipt extraction with Analyze-Evolution.py in the same folder.
"""
import importlib.util
import json
import os
import sqlite3
import sys
from pathlib import Path


def read_db(path, queries, optional=None):
    with sqlite3.connect(path.resolve().as_uri() + '?mode=ro', uri=True) as db:
        db.row_factory = sqlite3.Row
        result = {name: [dict(row) for row in db.execute(sql)] for name, sql in queries.items()}
        available = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        for name, (table, sql) in (optional or {}).items():
            result[name] = [dict(row) for row in db.execute(sql)] if table in available else None
        return result


def saved_files(run):
    """Saved variant owns hive files; saved controller owns meta records/logs."""
    configurations = {}
    for name in ('config.json', 'mq_config.json'):
        path = run / name
        if path.exists():
            configurations[name] = json.loads(path.read_text(encoding='utf-8-sig')).get('files', {})
    variant = configurations.get('config.json', configurations.get('mq_config.json', {}))
    controller = configurations.get('mq_config.json', variant)
    hive_state = run / variant.get('state', 'state.db')
    meta_state = run / controller.get('state', 'state.db')
    debug_paths, seen = [], set()
    for files in (controller, variant):
        path = run / files.get('debug', 'debug.db')
        identity = os.path.normcase(str(path.resolve()))
        if identity not in seen:
            seen.add(identity)
            debug_paths.append(path)
    return hive_state, meta_state, debug_paths


def main():
    run, rate_path, prefix = map(Path, sys.argv[1:])
    outputs = [Path(str(prefix) + suffix) for suffix in ('.json', '.md')]
    if any(path.exists() for path in outputs):
        raise FileExistsError('Analysis outputs already exist; select a new OUTPUT_PREFIX')
    spec = importlib.util.spec_from_file_location('saved_evolution_receipts', Path(__file__).with_name('Analyze-Evolution.py'))
    receipts = importlib.util.module_from_spec(spec)
    bytecode_setting = sys.dont_write_bytecode
    try:
        sys.dont_write_bytecode = True
        spec.loader.exec_module(receipts)
    finally:
        sys.dont_write_bytecode = bytecode_setting
    rates = json.loads(rate_path.read_text(encoding='utf-8-sig'))
    hive_state, meta_state, debug_paths = saved_files(run)
    state = read_db(hive_state, {
        'runs': 'SELECT id,started,finished,status,version,calls,request_id FROM hive_runs ORDER BY id',
        'drones': 'SELECT * FROM hive_drones ORDER BY id',
        'teams': 'SELECT * FROM hive_teams ORDER BY id',
        'members': 'SELECT * FROM hive_members ORDER BY team_id,drone_id',
        'commitments': 'SELECT * FROM hive_commitments ORDER BY created,drone_id',
        'requests': 'SELECT id,run_id,parent_id,contract,status,team_id,result,finish_reason,criteria_met,created FROM hive_requests ORDER BY id',
        'contributions': 'SELECT * FROM hive_contributions ORDER BY id',
        'links': 'SELECT * FROM hive_links ORDER BY id',
        'scores': 'SELECT * FROM hive_scores ORDER BY id',
        'guidance': 'SELECT * FROM hive_guidance ORDER BY id',
    }, optional={
        'assessments': ('hive_assessments', 'SELECT * FROM hive_assessments ORDER BY id'),
        'reviews': ('hive_reviews', 'SELECT * FROM hive_reviews ORDER BY id'),
    })
    state.update(read_db(meta_state, {}, optional={
        'meta_experiments': ('mq_experiments', 'SELECT * FROM mq_experiments ORDER BY id'),
        'meta_inquiries': ('mq_inquiries', 'SELECT * FROM mq_inquiries ORDER BY id'),
        'meta_commitments': ('mq_commitments', 'SELECT * FROM mq_commitments ORDER BY inquiry_id,drone_id'),
        'meta_assessments': ('mq_assessments', 'SELECT * FROM mq_assessments ORDER BY id'),
    }))
    log_queries = {
        'events': "SELECT 'hive_log' AS log_table,* FROM hive_log WHERE event NOT IN ('sql_start','sql_done') UNION ALL SELECT 'mq_log' AS log_table,* FROM mq_log WHERE event NOT IN ('sql_start','sql_done') ORDER BY ts,id",
        'sql_summary': 'SELECT event,sql_op,table_name,outcome,count(*) AS count FROM hive_log WHERE sql_op IS NOT NULL GROUP BY event,sql_op,table_name,outcome',
        'meta_queen_event_counts': 'SELECT event,outcome,count(*) AS count FROM mq_log GROUP BY event,outcome',
    }
    logs = {name: [] for name in log_queries}
    calls, aggregates, anomalies, failure_events = [], [], [], []
    for debug_path in debug_paths:
        for name, rows in read_db(debug_path, log_queries).items():
            logs[name].extend(row | {'source_db': debug_path.name, 'source_path': str(debug_path.resolve())} for row in rows)
        extracted, counts, errors, events = receipts.debug_receipts(debug_path, rates)
        calls.extend(extracted)
        aggregates.extend(counts)
        anomalies.extend(errors)
        failure_events.extend(events)
    logs['events'].sort(key=lambda event: (event['ts'], event['source_path'], event['log_table'], event['id']))
    for event in logs['events']:
        original = event['details']
        event['details'], error = receipts.decode(original)
        if error:
            event['details_raw'], event['details_parse_error'] = original, error
    calls.sort(key=lambda call: (call['started'], call['source_path'], call['log_table'], call['start_log_id']))
    for number, call in enumerate(calls, 1):
        call['number'] = number
    all_totals = receipts.totals(calls).get('all', {})
    totals = receipts.totals(calls, 'model')
    result = {'run_folder': str(run.resolve()), 'pricing': rates, 'state': state, 'logs': logs,
              'source_files': {'hive_state': str(hive_state.resolve()), 'meta_state': str(meta_state.resolve()),
                               'debug': [str(path.resolve()) for path in debug_paths]},
              'calls': calls, 'model_totals': totals,
              'by_role': receipts.totals(calls, 'role'),
              'by_source_db': receipts.totals(calls, 'source_db'),
              'estimated_total_usd': all_totals.get('known_estimated_usd'),
              'cost_basis': 'published-rate estimates; native billed dollars unavailable; missing measurements remain unknown',
              'event_and_sql_aggregates': aggregates, 'association_anomalies': anomalies,
              'failure_events': failure_events, 'rejections': receipts.rejection_summary(failure_events),
              'failures': [{'call_id': call['call_id'], 'failure_kinds': call['failure_kinds']} for call in calls if call['failure_kinds']],
              'unpriced_calls': [c['number'] for c in calls if c.get('estimated_usd') is None]}
    with Path(str(prefix)+'.json').open('x', encoding='utf-8') as output:
        json.dump(result, output, ensure_ascii=False, indent=2)
    lines = ['# Saved hive transcript and call receipts', '', f"Run: `{run.name}`", '',
             'Database access: read-only. No model calls. Costs are published-rate estimates using the saved pricing snapshot. '
             'Known totals exclude unpriced calls; missing usage remains unknown. Native billed dollars are unavailable in this normalized report; raw responses are retained.', '',
             '## Call costs', '', '| Call | Model | Role / decision / drone | Input | Cached | Output | Published-rate estimate USD | Log start / finish |',
             '|---:|---|---|---:|---:|---:|---:|---|']
    for call in calls:
        u = call['token_counters']
        activity = (call.get('decision') or {}).get('name') or call.get('prompt_key') or ''
        label = f"{call.get('role')} / {activity} / drone {call.get('drone_id')}"
        lines.append(f"| {call['number']} | {call['model']} | {label} | {u['input']} | {u['cached']} | {u['output']} | {call.get('estimated_usd')} | {call['source_db']}:{call['log_table']}: {call['start_log_id']} / {call.get('finish_log_id')} |")
    lines += ['', f"Known published-rate estimate USD: {result['estimated_total_usd']}; unpriced calls: {result['unpriced_calls']}", '']
    lines += receipts.assessment_markdown(state.get('meta_inquiries'), state.get('meta_assessments'))
    lines += receipts.rejection_markdown(failure_events)
    lines += ['## Contributions in saved order', '']
    for row in state['contributions']:
        lines += [f"### Contribution {row['id']} — drone {row['drone_id']} — {row['kind']}", '', f"Saved: {row['created']}; tick {row['tick']}", '', row['body'], '']
    lines += ['## Exact model exchanges', '']
    for call in calls:
        lines += [f"### Call {call['number']} — {call['source_db']}:{call['log_table']} {call['start_log_id']} → {call.get('finish_log_id')}", '',
                  f"Model: {call['model']}; outcome: {call.get('outcome')}; HTTP: {call.get('http_status')}", '']
        for msg in receipts.object_value(call.get('request')).get('messages') or []:
            lines += [f"#### {msg['role']}", '', str(msg.get('content')), '']
        if call.get('request_parse_error'):
            lines += ['#### Unparsed request', '', str(call.get('request_raw')), '']
        lines += ['#### Reply', '', call.get('reply') or call.get('response_raw') or str(call.get('error')), '']
    with Path(str(prefix)+'.md').open('x', encoding='utf-8') as output:
        output.write('\n'.join(lines))
    print(json.dumps({'calls': len(calls), 'model_totals': totals, 'by_role': result['by_role'], 'by_source_db': result['by_source_db'],
                      'estimated_total_usd': result['estimated_total_usd'], 'unpriced_calls': result['unpriced_calls'],
                      'rejection_event_counts': result['rejections']['event_counts']}, indent=2))


if __name__ == '__main__':
    main()
