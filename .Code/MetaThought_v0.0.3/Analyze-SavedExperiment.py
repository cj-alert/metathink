"""Read saved hive evidence without opening the application or making model calls.

Usage: python Analyze-SavedExperiment.py RUN_FOLDER RATES_JSON OUTPUT_PREFIX
SQLite connections are read-only. Output files must be new. Costs use returned
input/output/cache usage; reasoning tokens are a subset of output, not added.
This developer analysis utility never supplies diagnostics to the hive.
"""
import collections
import json
import sqlite3
import sys
from decimal import Decimal
from pathlib import Path


def read_db(path, queries):
    with sqlite3.connect(path.resolve().as_uri() + '?mode=ro', uri=True) as db:
        db.row_factory = sqlite3.Row
        return {name: [dict(row) for row in db.execute(sql)] for name, sql in queries.items()}


def main():
    run, rate_path, prefix = map(Path, sys.argv[1:])
    rates = json.loads(rate_path.read_text(encoding='utf-8-sig'))
    state = read_db(run / 'state.db', {
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
        'meta_experiments': 'SELECT * FROM mq_experiments ORDER BY id',
    })
    logs = read_db(run / 'debug.db', {
        'events': "SELECT 'hive_log' AS log_table,* FROM hive_log WHERE event NOT IN ('sql_start','sql_done') UNION ALL SELECT 'mq_log' AS log_table,* FROM mq_log WHERE event NOT IN ('sql_start','sql_done') ORDER BY ts,id",
        'sql_summary': 'SELECT event,sql_op,table_name,outcome,count(*) AS count FROM hive_log WHERE sql_op IS NOT NULL GROUP BY event,sql_op,table_name,outcome',
        'meta_queen_event_counts': 'SELECT event,outcome,count(*) AS count FROM mq_log GROUP BY event,outcome',
    })
    for event in logs['events']:
        event['details'] = json.loads(event['details'])
    calls, active, decision = [], None, None
    totals = {}
    for event in logs['events']:
        kind, details = event['event'], event['details']
        if kind == 'decision.question.start':
            decision = details
        elif kind == 'decision.question.finish':
            decision = None
        elif kind == 'llm.start':
            if active is not None:
                raise ValueError('Concurrent/interleaved calls need explicit association; sequential analysis cannot assume it')
            active = {'number': len(calls) + 1, 'log_table': event['log_table'], 'start_log_id': event['id'], 'started': event['ts'],
                      'scope': event['scope'], 'drone_id': event['drone_id'], 'model': details['model'],
                      'decision': decision, 'request': json.loads(details['request'])}
        elif kind == 'llm.response' and active is not None:
            active['response_log_id'] = event['id']
            active['http_status'] = details.get('status_code')
            active['response'] = json.loads(details['body'])
        elif kind == 'llm.finish':
            if active is None:
                raise ValueError('Completion without start')
            active.update(finish_log_id=event['id'], finished=event['ts'], outcome=details.get('outcome'),
                          usage=details.get('usage'), reply=details.get('reply'), error=details.get('error'),
                          duration_ms=details.get('duration_ms'))
            usage = active['usage'] or {}
            inp, out = usage.get('input_tokens'), usage.get('output_tokens')
            cached = (usage.get('input_tokens_details') or {}).get('cached_tokens')
            active['estimated_usd'] = None
            if all(isinstance(v, int) for v in (inp, out, cached)) and active['model'] in rates['models']:
                price = rates['models'][active['model']]
                if not 0 <= cached <= inp:
                    raise ValueError('Invalid cached/input token relationship')
                cost = ((inp-cached)*Decimal(str(price['input'])) + cached*Decimal(str(price['cached_input'])) + out*Decimal(str(price['output']))) / Decimal(1000000)
                active['estimated_usd'] = str(cost)
                total = totals.setdefault(active['model'], {'calls': 0, 'input': 0, 'cached': 0, 'output': 0, 'usd': Decimal(0)})
                for key, value in [('calls', 1), ('input', inp), ('cached', cached), ('output', out), ('usd', cost)]:
                    total[key] += value
            calls.append(active)
            active = None
    if active:
        calls.append(active)
    for total in totals.values():
        total['usd'] = str(total['usd'])
    result = {'run_folder': str(run.resolve()), 'pricing': rates, 'state': state, 'logs': logs,
              'calls': calls, 'model_totals': totals,
              'estimated_total_usd': str(sum((Decimal(t['usd']) for t in totals.values()), Decimal(0))),
              'unpriced_calls': [c['number'] for c in calls if c.get('estimated_usd') is None]}
    with Path(str(prefix)+'.json').open('x', encoding='utf-8') as output:
        json.dump(result, output, ensure_ascii=False, indent=2)
    lines = ['# Saved hive transcript and call receipts', '', f"Run: `{run.name}`", '',
             'Database access: read-only. No model calls. Costs are estimates using the saved pricing snapshot.', '',
             '## Call costs', '', '| Call | Model | Decision / drone | Input | Cached | Output | Estimated USD | Log start / finish |',
             '|---:|---|---|---:|---:|---:|---:|---|']
    for call in calls:
        u = call.get('usage') or {}
        label = (call.get('decision') or {}).get('name') or f"drone {call.get('drone_id')} / {call.get('scope')}"
        lines.append(f"| {call['number']} | {call['model']} | {label} | {u.get('input_tokens')} | {(u.get('input_tokens_details') or {}).get('cached_tokens')} | {u.get('output_tokens')} | {call.get('estimated_usd')} | {call['log_table']}: {call['start_log_id']} / {call.get('finish_log_id')} |")
    lines += ['', f"Estimated total USD: {result['estimated_total_usd']}", '', '## Contributions in saved order', '']
    for row in state['contributions']:
        lines += [f"### Contribution {row['id']} — drone {row['drone_id']} — {row['kind']}", '', f"Saved: {row['created']}; tick {row['tick']}", '', row['body'], '']
    lines += ['## Exact model exchanges', '']
    for call in calls:
        lines += [f"### Call {call['number']} — {call['log_table']} {call['start_log_id']} → {call.get('finish_log_id')}", '',
                  f"Model: {call['model']}; outcome: {call.get('outcome')}; HTTP: {call.get('http_status')}", '']
        for msg in call['request']['messages']:
            lines += [f"#### {msg['role']}", '', str(msg.get('content')), '']
        lines += ['#### Reply', '', call.get('reply') or str(call.get('error')), '']
    with Path(str(prefix)+'.md').open('x', encoding='utf-8') as output:
        output.write('\n'.join(lines))
    print(json.dumps({'calls': [{k: c.get(k) for k in ['number','start_log_id','finish_log_id','model','scope','drone_id','decision','usage','estimated_usd','outcome']} for c in calls], 'model_totals': totals, 'estimated_total_usd': result['estimated_total_usd'], 'unpriced_calls': result['unpriced_calls']}, indent=2))


if __name__ == '__main__':
    main()
