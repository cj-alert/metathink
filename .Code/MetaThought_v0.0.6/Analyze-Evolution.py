"""Read a saved MetaThought evolution campaign; never call a provider or write a DB.

Usage: python Analyze-Evolution.py RUN_FOLDER RATES_JSON OUTPUT_PREFIX
       python Analyze-Evolution.py --status RUN_FOLDER

JSON/Markdown outputs are exclusive new files. Pricing models use input,
cached_input and output USD per million tokens, as in Analyze-SavedExperiment.py.
Cached tokens are a subset of input; reasoning is already within output. The
imported reference's historical calls are excluded from campaign cost. Reads are
consistent within each database, not a simultaneous snapshot across databases.
"""
import argparse
import collections
import contextlib
import datetime
import json
import re
import sqlite3
from decimal import Decimal, InvalidOperation
from pathlib import Path


def decode(value):
    """Keep malformed provider bodies/log details, without aborting extraction."""
    if not isinstance(value, str):
        return value, None
    try:
        return json.loads(value), None
    except (ValueError, TypeError) as error:
        return None, str(error)


def object_value(value):
    return value if isinstance(value, dict) else {}


@contextlib.contextmanager
def readonly(path):
    db = sqlite3.connect(path.resolve().as_uri() + '?mode=ro', uri=True)
    db.row_factory = sqlite3.Row
    try:
        db.execute('BEGIN')
        yield db
    finally:
        db.close()


def rows(db, sql):
    return [dict(row) for row in db.execute(sql)]


def table_names(db):
    return {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}


def parsed_rows(data, fields):
    for row in data:
        for field in fields:
            original = row.get(field)
            value, error = decode(original)
            row[field] = value
            if error:
                row[field + '_raw'] = original
                row[field + '_parse_error'] = error
    return data


def controller_state(path):
    with readonly(path) as db:
        return {
            'evolutions': parsed_rows(rows(db, 'SELECT * FROM mq_evolutions ORDER BY id'), ['state']),
            'methods': parsed_rows(rows(db, 'SELECT * FROM mq_methods ORDER BY id'), ['config']),
            'trials': parsed_rows(rows(db, 'SELECT * FROM mq_trials ORDER BY id'), ['evidence']),
        }


def hive_state(path):
    queries = {
        'runs': 'SELECT id,started,finished,status,version,calls,request_id FROM hive_runs ORDER BY id',
        'drones': 'SELECT * FROM hive_drones ORDER BY id',
        'teams': 'SELECT * FROM hive_teams ORDER BY id',
        'members': 'SELECT * FROM hive_members ORDER BY team_id,drone_id',
        'commitments': 'SELECT * FROM hive_commitments ORDER BY created,drone_id',
        'requests': 'SELECT * FROM hive_requests ORDER BY id',
        'contributions': 'SELECT * FROM hive_contributions ORDER BY id',
        'links': 'SELECT * FROM hive_links ORDER BY id',
        'scores': 'SELECT * FROM hive_scores ORDER BY id',
        'guidance': 'SELECT * FROM hive_guidance ORDER BY id',
    }
    with readonly(path) as db:
        available = table_names(db)
        return {key: rows(db, query) for key, query in queries.items() if 'hive_' + key in available}


def instruction_map(config):
    config = object_value(config)
    result = {'prompt.' + key: value for key, value in object_value(config.get('prompts')).items()}
    for index, chromosome in enumerate(config.get('chromosomes') or []):
        for behavior in ('contribute', 'expand'):
            result[f'chromosome.{index}.{behavior}'] = object_value(chromosome).get(behavior)
    return result


def method_changes(methods):
    by_id = {method['id']: method for method in methods}
    output = []
    for method in methods:
        before = instruction_map(object_value(by_id.get(method.get('parent_id'))).get('config'))
        after = instruction_map(method.get('config'))
        changes = []
        if method.get('parent_id'):
            changes = [{'instruction': key, 'before': before.get(key), 'after': after.get(key)}
                       for key in sorted(before.keys() | after.keys()) if before.get(key) != after.get(key)]
        output.append({key: method.get(key) for key in
                       ('id', 'parent_id', 'donor_id', 'operator', 'target', 'chromosome', 'created')} | {'changes': changes})
    return output


def trial_number(path):
    match = re.fullmatch(r'trial-(\d+)-(?:state|debug)\.db', path.name)
    return int(match.group(1)) if match else None


def trial_origin(trial, methods, local):
    if local:
        return 'local_hive'
    method = next((item for item in methods if item['id'] == trial['method_id']), {})
    if method.get('operator') == 'reference' and trial.get('status') == 'completed':
        return 'imported_reference'
    return 'no_local_hive_state'


def normalized_raw_usage(response):
    usage = object_value(object_value(response).get('usage'))
    return {
        'input_tokens': usage.get('prompt_tokens', usage.get('input_tokens')),
        'output_tokens': usage.get('completion_tokens', usage.get('output_tokens')),
        'total_tokens': usage.get('total_tokens'),
        'input_tokens_details': object_value(usage.get('prompt_tokens_details', usage.get('input_tokens_details'))),
        'output_tokens_details': object_value(usage.get('completion_tokens_details', usage.get('output_tokens_details'))),
    }


def usage_values(usage):
    usage = object_value(usage)
    return {
        'input': usage.get('input_tokens'), 'output': usage.get('output_tokens'),
        'total': usage.get('total_tokens'),
        'cached': object_value(usage.get('input_tokens_details')).get('cached_tokens'),
        'cache_write': object_value(usage.get('input_tokens_details')).get('cache_write_tokens'),
        'reasoning': object_value(usage.get('output_tokens_details')).get('reasoning_tokens'),
    }


def integer(value):
    return isinstance(value, int) and not isinstance(value, bool) and value >= 0


def price_call(call, rates):
    raw_usage = normalized_raw_usage(call.get('response'))
    saved_values, raw_values = usage_values(call.get('usage')), usage_values(raw_usage)
    values, sources, checks = {}, {}, []
    for key in saved_values:
        saved, raw = saved_values[key], raw_values[key]
        values[key] = saved if saved is not None else raw
        sources[key] = 'llm.finish' if saved is not None else ('llm.response' if raw is not None else 'unknown')
        if saved is not None and raw is not None and saved != raw:
            checks.append({'kind': 'usage_mismatch', 'counter': key, 'finish': saved, 'response': raw})
        if values[key] is not None and not integer(values[key]):
            checks.append({'kind': 'invalid_counter', 'counter': key, 'value': values[key]})
    inp, out, cached, reasoning, total = (values[key] for key in ('input', 'output', 'cached', 'reasoning', 'total'))
    if integer(inp) and integer(cached) and cached > inp:
        checks.append({'kind': 'cached_exceeds_input'})
    if integer(out) and integer(reasoning) and reasoning > out:
        checks.append({'kind': 'reasoning_exceeds_output'})
    if all(integer(v) for v in (inp, out, total)) and total != inp + out:
        checks.append({'kind': 'total_differs_from_input_plus_output'})
    call.update(raw_usage=raw_usage, token_counters=values, usage_sources=sources,
                usage_checks=checks, estimated_usd=None, unpriced_reason=None)
    if call.get('finish_log_id') is None:
        call['unpriced_reason'] = 'unfinished_call'
    elif any(check['kind'] in ('usage_mismatch', 'invalid_counter', 'cached_exceeds_input') for check in checks):
        call['unpriced_reason'] = 'invalid_or_conflicting_usage'
    elif not all(integer(v) for v in (inp, out, cached)):
        call['unpriced_reason'] = 'missing_input_output_or_cached_usage'
    else:
        model_rate = object_value(object_value(rates.get('models')).get(call.get('model')))
        try:
            prices = [Decimal(str(model_rate[key])) for key in ('input', 'cached_input', 'output')]
            if any(not value.is_finite() or value < 0 for value in prices):
                raise ValueError('invalid rate')
            call['estimated_usd'] = str(((inp-cached)*prices[0] + cached*prices[1] + out*prices[2]) / Decimal(1000000))
        except (KeyError, InvalidOperation, ValueError):
            call['unpriced_reason'] = 'missing_or_invalid_model_rate'


def call_failures(call):
    kinds = []
    status = call.get('http_status')
    if isinstance(status, int) and not 200 <= status < 300:
        kinds.append('http_rejection')
    if call.get('refusal') or call.get('outcome') == 'refusal':
        kinds.append('provider_refusal')
    decision = object_value(call.get('decision_finish'))
    if decision.get('outcome') == 'refusal':
        kinds.append('decisions_refusal')
    if decision.get('outcome') == 'format_error' and call.get('outcome') == 'answered':
        kinds.append('decision_parser_error')
    if call.get('provider_error_kind'):
        kinds.append(call['provider_error_kind'])
    elif call.get('error'):
        kinds.append('provider_error')
    if call.get('finish_log_id') is None:
        kinds.append('unfinished_call')
    return sorted(set(kinds))


def debug_receipts(path, rates):
    """Associate sequential calls separately in each DB/table; retain anomalies."""
    calls, summaries, anomalies, failure_events = [], [], [], []
    with readonly(path) as db:
        for table in ('hive_log', 'mq_log'):
            if table not in table_names(db):
                continue
            event_counts = rows(db, f'SELECT event,outcome,count(*) AS count FROM {table} GROUP BY event,outcome ORDER BY event,outcome')
            sql_counts = rows(db, f'SELECT event,sql_op,table_name,outcome,count(*) AS count FROM {table} WHERE sql_op IS NOT NULL GROUP BY event,sql_op,table_name,outcome ORDER BY event,sql_op,table_name,outcome')
            summaries.append({'source_db': path.name, 'log_table': table, 'event_counts': event_counts, 'sql_counts': sql_counts})
            active, decision, decision_call, meta_prompt = None, None, None, None
            query = f"SELECT * FROM {table} WHERE event NOT IN ('sql_start','sql_done') ORDER BY id"
            for event in rows(db, query):
                details, parse_error = decode(event.get('details'))
                details = object_value(details)
                receipt = {'source_db': path.name, 'log_table': table, 'log_id': event['id'], 'ts': event['ts']}
                if parse_error:
                    anomalies.append(receipt | {'kind': 'invalid_log_details', 'raw': event.get('details'), 'error': parse_error})
                kind = event['event']
                if details.get('error') or details.get('outcome') in ('refusal', 'format_error', 'error', 'failed'):
                    failure_events.append(receipt | {'event': kind, 'scope': event.get('scope'), 'details': details})
                if kind == 'decision.question.start':
                    decision = details | {'start_log_id': event['id']}
                    decision_call = None
                elif kind == 'decision.question.finish':
                    if decision_call is not None:
                        decision_call.update(decision_finish=details, decision_finish_log_id=event['id'])
                    else:
                        anomalies.append(receipt | {'kind': 'decision_finish_without_call', 'details': details})
                    decision, decision_call = None, None
                elif kind == 'meta_call_start':
                    meta_prompt = details.get('prompt_key')
                elif kind == 'meta_call_done':
                    meta_prompt = None
                elif kind == 'llm.start':
                    if active is not None:
                        anomalies.append(receipt | {'kind': 'new_start_before_previous_finish', 'previous_start': active['start_log_id']})
                    request, error = decode(details.get('request'))
                    active = {
                        'source_db': path.name, 'log_table': table, 'trial_id': trial_number(path),
                        'call_id': f'{path.name}:{table}:{event["id"]}', 'start_log_id': event['id'],
                        'started': event['ts'], 'scope': event.get('scope'), 'role': event.get('scope'),
                        'drone_id': event.get('drone_id'), 'request_id': event.get('request_id'),
                        'run_id': event.get('run_id'), 'team_id': event.get('team_id'),
                        'model': details.get('model') or event.get('model'), 'decision': decision,
                        'prompt_key': meta_prompt, 'request': request, 'request_raw': details.get('request'),
                        'request_parse_error': error, 'responses': [], 'outcome': 'in_progress',
                    }
                    calls.append(active)
                    if decision is not None:
                        decision_call = active
                elif kind == 'llm.response':
                    response, error = decode(details.get('body'))
                    response_record = receipt | {'http_status': details.get('status_code'), 'body': response,
                                                  'body_raw': details.get('body'), 'parse_error': error}
                    if active is None:
                        anomalies.append(response_record | {'kind': 'response_without_start'})
                    else:
                        active['responses'].append(response_record)
                        active.update(response_log_id=event['id'], http_status=details.get('status_code'),
                                      response=response, response_raw=details.get('body'), response_parse_error=error)
                elif kind == 'llm.finish':
                    if active is None:
                        anomalies.append(receipt | {'kind': 'finish_without_start', 'details': details})
                    else:
                        actual_model = object_value(active.get('response')).get('model')
                        active.update(finish_log_id=event['id'], finished=event['ts'], outcome=details.get('outcome'),
                                      usage=details.get('usage'), reply=details.get('reply'), refusal=details.get('refusal'),
                                      error=details.get('error'), provider_error_kind=details.get('kind'),
                                      duration_ms=details.get('duration_ms'),
                                      model=actual_model or details.get('model') or active.get('model'))
                        if active.get('http_status') is None and details.get('status_code'):
                            active['http_status'] = details['status_code']
                        active = None
    for call in calls:
        price_call(call, rates)
        call['failure_kinds'] = call_failures(call)
    return calls, summaries, anomalies, failure_events


def totals(calls, field=None):
    groups = collections.defaultdict(list)
    for call in calls:
        value = call.get(field) if field else 'all'
        if field == 'trial_id':
            value = 'controller' if value is None else f'trial-{value}'
        groups[str(value) if value is not None else 'unknown'].append(call)
    result = {}
    for key, group in groups.items():
        priced = [call for call in group if call.get('estimated_usd') is not None]
        counters = {}
        for counter in ('input', 'cached', 'cache_write', 'output', 'reasoning', 'total'):
            values = [call['token_counters'][counter] for call in group if integer(call['token_counters'][counter])]
            counters[counter] = {'known_sum': sum(values) if values else None,
                                 'unknown_calls': len(group) - len(values)}
        result[key] = {'calls': len(group), 'completed_receipts': sum(call.get('finish_log_id') is not None for call in group),
                       'priced_calls': len(priced), 'unpriced_calls': len(group)-len(priced),
                       'known_estimated_usd': str(sum((Decimal(call['estimated_usd']) for call in priced), Decimal(0))) if priced else None,
                       'tokens': counters}
    return result


def compact_status(run):
    state = controller_state(run / 'state.db')
    result = {'run_folder': str(run.resolve()), 'observed_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'evolutions': [{key: row.get(key) for key in ('id', 'status', 'updated')} |
                             {key: object_value(row.get('state')).get(key) for key in ('phase', 'action', 'method_id', 'trial_id', 'target', 'error')}
                             for row in state['evolutions']],
              'methods': method_changes(state['methods']), 'trials': []}
    for trial in state['trials']:
        item = {key: trial.get(key) for key in ('id', 'method_id', 'status', 'updated')}
        path = run / f'trial-{trial["id"]}-state.db'
        item['origin'] = trial_origin(trial, state['methods'], path.exists())
        if path.exists():
            with readonly(path) as db:
                item['requests'] = rows(db, 'SELECT id,status,team_id,criteria_met,finish_reason FROM hive_requests ORDER BY id')
                item['latest_contribution'] = rows(db, 'SELECT id,request_id,drone_id,tick,kind,created FROM hive_contributions ORDER BY id DESC LIMIT 1')
        result['trials'].append(item)
    result['latest_activity'] = []
    for path in [run / 'debug.db'] + sorted(run.glob('trial-*-debug.db'), key=lambda item: trial_number(item) or 0):
        if not path.exists():
            continue
        with readonly(path) as db:
            for table in ('hive_log', 'mq_log'):
                if table not in table_names(db):
                    continue
                latest = rows(db, f"SELECT id,ts,scope,event,outcome,details FROM {table} WHERE event NOT IN ('sql_start','sql_done') ORDER BY id DESC LIMIT 1")
                starts = rows(db, f"SELECT id,ts,scope,model FROM {table} WHERE event='llm.start' ORDER BY id DESC LIMIT 1")
                question = rows(db, f"SELECT id,details FROM {table} WHERE event='decision.question.start' ORDER BY id DESC LIMIT 1")
                counts = rows(db, f"SELECT event,count(*) AS count FROM {table} WHERE event IN ('llm.start','llm.finish','llm.response') GROUP BY event")
                if latest:
                    event = latest[0]
                    details = object_value(decode(event.pop('details'))[0])
                    event['context'] = {key: details[key] for key in ('phase', 'prompt_key', 'method_id', 'trial_id', 'error') if key in details}
                    if question:
                        q = object_value(decode(question[0]['details'])[0])
                        event['latest_decision'] = {key: q.get(key) for key in ('name', 'class')}
                    result['latest_activity'].append({'source_db': path.name, 'log_table': table, 'latest': event,
                                                      'latest_llm_start': starts, 'call_events': counts})
    return result


def markdown(result):
    lines = ['# Saved evolution campaign analysis', '', f"Run: `{result['run_folder']}`", '',
             'Read-only saved evidence; no model calls. Costs are estimates from the supplied pricing snapshot. '
             'Known cost excludes unpriced calls and imported historical reference calls. Cached tokens are within input; reasoning is within output.', '',
             '## Campaign state', '', '```json', json.dumps(result['controller']['evolutions'], ensure_ascii=False, indent=2), '```', '',
             '## Methods and exact instruction changes', '']
    for method in result['method_changes']:
        lines += [f"### Method {method['id']} — {method['operator']}", '',
                  f"Parent: {method['parent_id']}; donor: {method['donor_id']}; target: {method['target']}", '']
        for change in method['changes']:
            lines += [f"Instruction: `{change['instruction']}`", '', '**Before**', '', str(change['before']), '', '**After**', '', str(change['after']), '']
    lines += ['## Saved comparative assessments', '']
    for trial in result['controller']['trials']:
        lines += [f"### Trial {trial['id']} — method {trial['method_id']} — {trial['status']} — {trial['origin']}", '', trial.get('assessment') or '(No assessment saved.)', '']
    lines += ['## Call and cost receipts', '', '```json', json.dumps(result['totals'], indent=2), '```', '',
              '| Call | Source | Role / decision | Model | Outcome | Input | Cached | Output | Estimated USD |',
              '|---:|---|---|---|---|---:|---:|---:|---:|']
    for call in result['calls']:
        v = call['token_counters']
        label = object_value(call.get('decision')).get('name') or call.get('prompt_key') or call.get('role')
        cells = [call['number'], call['call_id'], label, call['model'], ', '.join(call['failure_kinds']) or call['outcome'],
                 v['input'], v['cached'], v['output'], call['estimated_usd']]
        lines.append('| ' + ' | '.join(str(value).replace('|', '\\|').replace('\n', ' ') for value in cells) + ' |')
    lines += ['', '## Failures and incomplete calls', '', '```json', json.dumps(result['failures'], ensure_ascii=False, indent=2), '```', '',
              '## Attributed trial contributions', '']
    for trial in result['actual_trials']:
        for row in trial['state'].get('contributions', []):
            lines += [f"### Trial {trial['trial_id']}, contribution {row['id']} — drone {row['drone_id']} — {row['kind']} — tick {row['tick']}", '', row['body'], '']
    lines += ['## Exact model exchanges', '']
    for call in result['calls']:
        lines += [f"### Call {call['number']} — {call['call_id']}", '',
                  f"Response log: {call.get('response_log_id')}; finish log: {call.get('finish_log_id')}; HTTP: {call.get('http_status')}", '',
                  '#### Request', '', '```json', json.dumps(call.get('request'), ensure_ascii=False, indent=2) if call.get('request_parse_error') is None else str(call.get('request_raw')), '```', '',
                  '#### Reply', '', call.get('reply') or call.get('response_raw') or call.get('error') or '(No response receipt.)', '']
    return '\n'.join(lines)


def analyze(run, rates):
    controller = controller_state(run / 'state.db')
    actual_trials = [{'trial_id': trial_number(path), 'source_db': path.name, 'state': hive_state(path)}
                     for path in sorted(run.glob('trial-*-state.db'), key=lambda item: trial_number(item) or 0)
                     if trial_number(path) is not None]
    local_ids = {trial['trial_id'] for trial in actual_trials}
    for trial in controller['trials']:
        trial['origin'] = trial_origin(trial, controller['methods'], trial['id'] in local_ids)
    calls, summaries, anomalies, failure_events = [], [], [], []
    for path in [run / 'debug.db'] + sorted(run.glob('trial-*-debug.db'), key=lambda item: trial_number(item) or 0):
        if path.exists():
            extracted, counts, errors, events = debug_receipts(path, rates)
            calls.extend(extracted)
            summaries.extend(counts)
            anomalies.extend(errors)
            failure_events.extend(events)
    calls.sort(key=lambda call: (call['started'], call['source_db'], call['log_table'], call['start_log_id']))
    for number, call in enumerate(calls, 1):
        call['number'] = number
    failures = [{key: call.get(key) for key in ('number', 'call_id', 'trial_id', 'model', 'failure_kinds', 'http_status', 'error', 'refusal', 'decision_finish')}
                for call in calls if call['failure_kinds']]
    return {'run_folder': str(run.resolve()), 'observed_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
            'pricing': rates, 'controller': controller, 'method_changes': method_changes(controller['methods']),
            'actual_trials': actual_trials, 'calls': calls, 'event_and_sql_aggregates': summaries,
            'totals': {'campaign': totals(calls), 'by_model': totals(calls, 'model'),
                       'by_role': totals(calls, 'role'), 'by_trial': totals(calls, 'trial_id')},
            'failures': failures, 'failure_events': failure_events, 'association_anomalies': anomalies,
            'failure_counts': dict(collections.Counter(kind for call in calls for kind in call['failure_kinds'])),
            'unpriced_calls': [call['call_id'] for call in calls if call['estimated_usd'] is None]}


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('run_folder', nargs='?', type=Path)
    parser.add_argument('rates_json', nargs='?', type=Path)
    parser.add_argument('output_prefix', nargs='?', type=Path)
    parser.add_argument('--status', type=Path, metavar='RUN_FOLDER')
    args = parser.parse_args()
    if args.status:
        if any((args.run_folder, args.rates_json, args.output_prefix)):
            parser.error('--status uses only RUN_FOLDER and writes no artifacts')
        print(json.dumps(compact_status(args.status), ensure_ascii=False, separators=(',', ':')))
        return
    if not all((args.run_folder, args.rates_json, args.output_prefix)):
        parser.error('provide RUN_FOLDER RATES_JSON OUTPUT_PREFIX, or --status RUN_FOLDER')
    json_path, md_path = (Path(str(args.output_prefix) + suffix) for suffix in ('.json', '.md'))
    if json_path.exists() or md_path.exists():
        parser.error('analysis outputs already exist; select a new OUTPUT_PREFIX')
    rates = json.loads(args.rates_json.read_text(encoding='utf-8-sig'))
    result = analyze(args.run_folder, rates)
    # Both files use exclusive creation; an interrupted partial report is retained.
    with json_path.open('x', encoding='utf-8') as output:
        json.dump(result, output, ensure_ascii=False, indent=2)
    with md_path.open('x', encoding='utf-8') as output:
        output.write(markdown(result))
    print(json.dumps({'outputs': [str(json_path.resolve()), str(md_path.resolve())],
                      'campaign': result['totals']['campaign'], 'by_model': result['totals']['by_model'],
                      'by_role': result['totals']['by_role'], 'by_trial': result['totals']['by_trial'],
                      'failure_counts': result['failure_counts'], 'unpriced_calls': result['unpriced_calls'],
                      'association_anomaly_count': len(result['association_anomalies'])}, separators=(',', ':')))


if __name__ == '__main__':
    main()
