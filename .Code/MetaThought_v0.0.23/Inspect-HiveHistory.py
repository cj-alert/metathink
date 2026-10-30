"""Read-only, bounded hive-history comparison; never loads provider transcripts."""
import argparse
from collections import Counter
import datetime as dt
import json
from pathlib import Path
import sqlite3


def stamp(value):
    return dt.datetime.fromisoformat(value.replace('Z', '+00:00'))


def read_history(folder):
    folder = folder.resolve()
    cfg = json.loads((folder / 'config.json').read_text(encoding='utf-8-sig'))
    def connect(name):
        db = sqlite3.connect((folder / name).resolve().as_uri() + '?mode=ro', uri=True)
        db.row_factory = sqlite3.Row
        return db
    with connect(cfg['files']['state']) as db:
        runs = [dict(r) for r in db.execute('SELECT id,version,started,status,config FROM hive_runs ORDER BY id')]
        work = [dict(r) for r in db.execute('SELECT id,inquiry_id,drone_id,phase,kind,created FROM hive_inquiry_work ORDER BY id')]
        members = [dict(r) for r in db.execute('SELECT inquiry_id,drone_id,created FROM hive_inquiry_members ORDER BY created,inquiry_id,drone_id')]
        inquiries = [dict(r) for r in db.execute('SELECT id,proposed_by,created FROM hive_inquiries ORDER BY id')]
        final_population = db.execute('SELECT COUNT(*) FROM hive_drones').fetchone()[0]
    initial_cfg = json.loads(runs[0]['config'])
    seeds = initial_cfg['hive']['drone_count']
    with connect(cfg['files']['debug']) as db:
        calls = [dict(r) for r in db.execute("SELECT id,ts,scope,drone_id,json_extract(details,'$.model') AS model,json_extract(details,'$.phase') AS phase FROM hive_log WHERE event='llm.start' ORDER BY id")]
        finishes = [dict(r) for r in db.execute("SELECT id,ts,json_extract(details,'$.outcome') AS outcome FROM hive_log WHERE event='llm.finish' ORDER BY id")]
        errors = [dict(r) for r in db.execute("SELECT id,ts,event,json_extract(details,'$.error') AS error,json_extract(details,'$.result.error') AS result_error FROM hive_log WHERE event NOT IN ('sql_start','sql_done','llm.start','llm.response','llm.finish') AND (json_extract(details,'$.error') IS NOT NULL OR json_extract(details,'$.result.error') IS NOT NULL) ORDER BY id")]
        first_requests = []
        for scope in ('drone', 'queen'):
            row = db.execute("SELECT id,ts,json_extract(details,'$.request') AS request FROM hive_log WHERE event='llm.start' AND scope=? ORDER BY id LIMIT 1", (scope,)).fetchone()
            if row:
                request = json.loads(row['request']) if isinstance(row['request'], str) else row['request']
                user_messages = [m.get('content') for m in request.get('messages', []) if m.get('role') == 'user']
                try:
                    initial_input = json.loads(user_messages[0]) if user_messages and isinstance(user_messages[0], str) else {}
                except json.JSONDecodeError:
                    initial_input = {}
                handoff = initial_input.get('handoff') if isinstance(initial_input, dict) else None
                first_requests.append({'scope': scope, 'log_id': row['id'], 'ts': row['ts'], 'model': request.get('model'), 'initial_handoff': handoff})
    def at(row):
        cutoff = stamp(row['created'])
        committed = {}
        for member in members:
            if stamp(member['created']) <= cutoff:
                committed.setdefault(member['inquiry_id'], set()).add(member['drone_id'])
        visible = [q for q in inquiries if q['proposed_by'] and stamp(q['created']) <= cutoff]
        prior_work = [w for w in work if w['id'] <= row['id']]
        births = sum(w['kind'] == 'SPAWN' for w in prior_work)
        return {'through_work_id': row['id'], 'work_records': len(prior_work), 'utc': row['created'],
                'drones': seeds + births, 'births': births,
                'research_inquiries': len(visible),
                'teams': sum(len(committed.get(q['id'], set())) >= 2 for q in visible),
                'membership_sizes': {str(q['id']): len(committed.get(q['id'], set())) for q in visible},
                'calls_started': sum(stamp(c['ts']) <= cutoff for c in calls),
                'calls_finished': sum(stamp(c['ts']) <= cutoff for c in finishes)}
    checkpoints = [at(work[n-1]) for n in (5,10,20,44,50,100,132,150,220) if len(work) >= n]
    if work and (not checkpoints or checkpoints[-1]['work_records'] != len(work)):
        checkpoints.append(at(work[-1]))
    birth_work = [w for w in work if w['kind'] == 'SPAWN']
    return {'run_folder': str(folder), 'initial_version': runs[0]['version'], 'current_version': cfg['version'],
            'initial_drones': seeds, 'final_drones': final_population, 'final_work_records': len(work),
            'initial_models': initial_cfg['models'], 'current_models': cfg['models'],
            'initial_chromosomes': initial_cfg.get('chromosomes'), 'initial_workers': initial_cfg.get('workers'),
            'initial_prompts': {k: initial_cfg.get('prompts', {}).get(k) for k in ('inquiry_contribute','inquiry_expand','inquiry_actions')},
            'checkpoints': checkpoints, 'first_birth': at(birth_work[0]) if birth_work else None,
            'birth_work': birth_work, 'proposals': [w for w in work if w['kind'] == 'PROPOSE'],
            'joins': [w for w in work if w['kind'] == 'JOIN'], 'first_work': work[:20],
            'first_requests': first_requests, 'runtime_errors': errors,
            'action_counts': dict(Counter(w['kind'] for w in work)),
            'calls_started': len(calls), 'calls_finished': len(finishes),
            'query_scope': 'Metadata and selected initial configuration only; no full provider payloads, no writes.'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run', action='append', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    if args.out.exists():
        raise FileExistsError('Preserve existing history evidence')
    report = {'generated_utc': dt.datetime.now(dt.timezone.utc).isoformat(), 'runs': [read_history(p) for p in args.run]}
    with args.out.open('x', encoding='utf-8') as output:
        json.dump(report, output, indent=2)
    print(json.dumps({'out': str(args.out), 'runs': [{'folder': r['run_folder'], 'initial_version': r['initial_version'], 'first_birth': r['first_birth'], 'checkpoints': r['checkpoints']} for r in report['runs']]}))


if __name__ == '__main__':
    main()
