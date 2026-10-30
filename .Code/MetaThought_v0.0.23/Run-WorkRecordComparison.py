"""External experiment observer. The hive itself has no work/call budget."""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import time


def utc():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def save(path, value):
    path = Path(path)
    temporary = path.with_name(path.name + '.tmp')
    temporary.write_text(json.dumps(value, indent=2) + '\n', encoding='utf-8')
    os.replace(temporary, path)


def snapshot(run):
    cfg = json.loads((run / 'config.json').read_text(encoding='utf-8-sig'))
    path = run / cfg['files']['state']
    if not path.is_file():
        return {'status': 'initializing', 'work_records': 0}
    with sqlite3.connect(path.resolve().as_uri() + '?mode=ro', uri=True) as db:
        db.row_factory = sqlite3.Row
        tables = {r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        needed = {'hive_requests', 'hive_inquiry_work', 'hive_drones', 'hive_inquiries',
                  'hive_inquiry_members', 'hive_worker_queue', 'hive_worker_queen'}
        if not needed.issubset(tables):
            return {'status': 'initializing', 'work_records': 0}
        row = db.execute("""SELECT r.id AS request_id,r.status,r.finish_reason,
          (SELECT COUNT(*) FROM hive_inquiry_work) AS work_records,
          (SELECT COUNT(*) FROM hive_drones) AS drones,
          (SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='SPAWN') AS births,
          (SELECT COUNT(*) FROM hive_inquiries WHERE proposed_by<>0) AS research_inquiries,
          (SELECT COUNT(*) FROM (SELECT i.id FROM hive_inquiries i
             JOIN hive_inquiry_members m ON m.inquiry_id=i.id WHERE i.proposed_by<>0
             GROUP BY i.id HAVING COUNT(DISTINCT m.drone_id)>=2)) AS committed_teams,
          (SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>'') AS owned_drone_claims,
          (SELECT COUNT(*) FROM hive_worker_queen WHERE owner<>'') AS queen_claims
          FROM hive_requests r ORDER BY r.id DESC LIMIT 1""").fetchone()
        value = dict(row) if row else {'status': 'initializing', 'work_records': 0}
    debug_path = run / cfg['files']['debug']
    if debug_path.is_file():
        with sqlite3.connect(debug_path.resolve().as_uri() + '?mode=ro', uri=True) as debug:
            counts = dict(debug.execute("SELECT event,COUNT(*) FROM hive_log WHERE event IN ('llm.start','llm.finish') GROUP BY event"))
        value['calls_started'] = counts.get('llm.start', 0)
        value['calls_finished'] = counts.get('llm.finish', 0)
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', type=Path)
    parser.add_argument('--inspect', type=Path)
    args = parser.parse_args()
    if args.inspect:
        print(json.dumps(snapshot(args.inspect.resolve())))
        return
    plan = json.loads(args.plan.read_text(encoding='utf-8-sig'))
    progress_path, log_path = Path(plan['progress_file']), Path(plan['event_log'])
    if progress_path.exists() or log_path.exists():
        raise FileExistsError('Preserve an existing observation; do not start its plan twice')
    state = {'started_utc': utc(), 'status': 'running', 'active_run': None, 'results': []}
    flags = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0
    with log_path.open('x', encoding='utf-8', buffering=1) as log:
        def event(name, **values):
            log.write(json.dumps({'utc': utc(), 'event': name, **values}) + '\n')
        def publish(name):
            save(progress_path, state)
            active = state['active_run']
            visible = {'event': name, 'status': state['status'],
                       'active': {'version': active['version'], 'pid': active.get('pid'), **(active.get('snapshot') or {})} if active else None,
                       'stopped': [{'version': r['version'], 'reason': r['stop_reason'], 'exit_code': r['exit_code'], **r['final_snapshot']} for r in state['results']]}
            if not (plan.get('quiet') and name == 'progress'):
                print(json.dumps(visible), flush=True)
        publish('comparison_started')
        for item in plan['runs']:
            if Path(plan['stop_file']).exists():
                state['status'] = 'operator_stopped'
                break
            run = Path(item['run_folder']).resolve()
            cfg = json.loads((run / 'config.json').read_text(encoding='utf-8-sig'))
            mode = item.get('mode', 'fresh')
            state_exists = (run / cfg['files']['state']).exists()
            if mode == 'fresh' and state_exists:
                raise FileExistsError('Fresh observation refuses an existing state database: ' + str(run))
            if mode == 'resume' and not state_exists:
                raise FileNotFoundError('Resume requires its saved state: ' + str(run))
            baseline = snapshot(run) if state_exists else {'calls_finished': 0}
            call_target = baseline['calls_finished'] + plan['additional_completed_calls'] if 'additional_completed_calls' in plan else None
            exe = str(run / 'MetaThought.exe')
            argv = [exe, 'work', '-dir', str(run)] if mode == 'resume' else [exe, 'workers', '-config', str(run / 'config.json'),
                    '-dir', str(run), '-handoff', str(run / 'handoff.md'), '-confirm']
            prefix = 'workers-continue-' + dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ') if mode == 'resume' else 'workers'
            output_path, error_path = run / (prefix + '.stdout.txt'), run / (prefix + '.stderr.txt')
            started = utc()
            state['active_run'] = {**item, 'started_utc': started, 'snapshot': None, 'baseline': baseline, 'call_target': call_target}
            boundary = None
            operator_stop = False
            previous_display = None
            pause_results = []
            process = None
            def pause():
                reply = subprocess.run([exe, 'worker-pause', '-dir', str(run)], cwd=run,
                                       capture_output=True, text=True, creationflags=flags)
                value = {'utc': utc(), 'exit_code': reply.returncode,
                         'stdout': reply.stdout, 'stderr': reply.stderr}
                pause_results.append(value)
                event('pause_requested', version=item['version'], **value)
                if reply.returncode:
                    raise RuntimeError('Native pause failed: ' + reply.stderr)
            with output_path.open('x', encoding='utf-8') as output, \
                 error_path.open('x', encoding='utf-8') as error:
                process = subprocess.Popen(argv, cwd=run, stdout=output, stderr=error, creationflags=flags)
                state['active_run']['pid'] = process.pid
                event('native_started', **state['active_run'])
                publish('run_started')
                try:
                    while process.poll() is None:
                        try:
                            current = snapshot(run)
                        except sqlite3.OperationalError as exc:
                            event('read_wait', version=item['version'], error=str(exc))
                            time.sleep(plan['poll_seconds'])
                            continue
                        event('sql_read', version=item['version'], tables='hive_requests/inquiry_work/drones/inquiries/inquiry_members/worker_queue/worker_queen', snapshot=current)
                        state['active_run']['snapshot'] = current
                        operator_stop = operator_stop or Path(plan['stop_file']).exists()
                        reached = current.get('calls_finished', 0) >= call_target if call_target is not None else current['work_records'] >= plan['work_records']
                        if reached and boundary is None:
                            boundary = {'utc': utc(), **current}
                            state['active_run']['boundary'] = boundary
                            event('work_boundary_reached', version=item['version'], boundary=boundary)
                            publish('work_boundary_reached')
                        if (boundary is not None or operator_stop) and current['status'] == 'working':
                            pause()
                        display = (current['status'], current['work_records'] // 25, current.get('births'))
                        if display != previous_display:
                            publish('progress')
                            previous_display = display
                        else:
                            save(progress_path, state)
                        time.sleep(plan['poll_seconds'])
                finally:
                    # On observer interruption/failure, safely stop its child; never orphan paid work.
                    while process.poll() is None:
                        pause()
                        time.sleep(plan['poll_seconds'])
            final = snapshot(run)
            result_files = sorted(run.glob('worker-result-*.json'))
            result = json.loads(result_files[-1].read_text(encoding='utf-8-sig')) if result_files else None
            reason = 'operator_stop' if operator_stop else ('call_boundary' if call_target is not None else 'work_boundary') if boundary else 'native_exit_before_boundary'
            receipt = {'operation': 'fresh workers with external work-record observation', 'executable': exe,
                       'config': str(run / 'config.json'), 'handoff': str(run / 'handoff.md'),
                       'arguments': argv[1:], 'working_directory': str(run), 'pid': process.pid,
                       'started_utc': started, 'finished_utc': utc(), 'exit_code': process.returncode,
                       'stdout': str(output_path), 'stderr': str(error_path),
                       'baseline': baseline, 'call_target': call_target,
                       'results': [str(p) for p in result_files], 'boundary': boundary, 'stop_reason': reason,
                       'pause_results': pause_results, 'final_snapshot': final, 'native_result': result}
            (run / (prefix + '.receipt.json')).write_text(json.dumps(receipt, indent=2) + '\n', encoding='utf-8')
            (run / (prefix + '.exitcode.txt')).write_text(str(process.returncode), encoding='utf-8')
            state['results'].append({'version': item['version'], 'run_folder': str(run), **receipt})
            state['active_run'] = None
            event('native_stopped', version=item['version'], final=final, reason=reason, exit_code=process.returncode)
            publish('run_stopped')
            if operator_stop:
                state['status'] = 'operator_stopped'
                break
        else:
            state['status'] = 'completed'
        state['finished_utc'] = utc()
        publish('comparison_stopped')


if __name__ == '__main__':
    main()
