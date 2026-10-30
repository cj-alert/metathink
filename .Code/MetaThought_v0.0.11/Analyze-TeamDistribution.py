"""Read-only, matched-prefix distribution comparison; never scores model answers."""
import argparse
import collections
import datetime
import json
from pathlib import Path
import sqlite3


def inspect(folder, cutoffs):
    connection = sqlite3.connect((folder.resolve() / 'state.db').as_uri() + '?mode=ro', uri=True)
    connection.row_factory = sqlite3.Row
    connection.execute('BEGIN')
    work = [dict(row) for row in connection.execute(
        'SELECT id,inquiry_id,target_inquiry_id,drone_id,phase,kind,created FROM hive_inquiry_work ORDER BY id')]
    drones = {row['id']: dict(row) for row in connection.execute('SELECT id,contribute,expand FROM hive_drones')}
    request = dict(connection.execute('SELECT id,status,finish_reason FROM hive_requests ORDER BY id DESC LIMIT 1').fetchone())
    maximum = work[-1]['id'] if work else 0
    sample = [dict(row) for row in connection.execute(
        "SELECT id,inquiry_id,target_inquiry_id,drone_id,phase,kind,body,created FROM hive_inquiry_work WHERE phase='expand' AND kind IN ('SPAWN','PROPOSE','JOIN','CONTRIBUTE','CONNECT') ORDER BY id DESC LIMIT 12")]
    claims = connection.execute("SELECT (SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>'')+(SELECT COUNT(*) FROM hive_worker_queen WHERE owner<>'')").fetchone()[0]
    connection.rollback()
    connection.close()
    points = []
    for cutoff in sorted(set(cutoffs + [maximum])):
        if cutoff > maximum or cutoff < 1:
            continue
        selected = [row for row in work if row['id'] <= cutoff]
        memberships = {(row['inquiry_id'], row['drone_id']) for row in selected if row['kind'] in ('PROPOSE', 'JOIN')}
        members = collections.Counter(inquiry for inquiry, drone in memberships)
        committed = {drone for inquiry, drone in memberships}
        actions = collections.Counter(row['kind'] for row in selected)
        expansion = collections.Counter(row['kind'] for row in selected if row['phase'] == 'expand')
        per_drone_members = collections.Counter(drone for inquiry, drone in memberships)
        spawn_instructions = collections.Counter(drones[row['drone_id']]['expand'] for row in selected if row['kind'] == 'SPAWN')
        largest = max(members.values(), default=0)
        points.append({
            'through_work_id': cutoff, 'saved_actions': len(selected),
            'committed_drones': len(committed), 'memberships': len(memberships),
            'members_by_inquiry': dict(sorted(members.items())),
            'teams_with_two_commitments': sum(count >= 2 for count in members.values()),
            'largest_team_members': largest,
            'largest_team_share': largest / len(committed) if committed else None,
            'overlapping_drones': sum(count > 1 for count in per_drone_members.values()),
            'actions': dict(actions), 'expand_actions': dict(expansion),
            'spawns_by_expand_instruction': dict(spawn_instructions),
        })
    return {'run': str(folder), 'request': request, 'max_work_id': maximum,
            'saved_claims': claims, 'snapshots': points, 'expand_examples': sample}


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--run', type=Path, required=True)
parser.add_argument('--compare', type=Path, required=True)
parser.add_argument('--out', type=Path, required=True)
arguments = parser.parse_args()
current = inspect(arguments.run, [100, 200, 400])
baseline = inspect(arguments.compare, [100, 200, 400, current['max_work_id']])
result = {'observed_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
          'method': 'Membership reconstructed from original PROPOSE/JOIN actions at matched work-id prefixes; overlap retained. Counts do not establish intellectual influence or answer quality.',
          'current': current, 'baseline': baseline}
with arguments.out.open('x', encoding='utf-8') as output:
    json.dump(result, output, ensure_ascii=False, indent=2)
print(json.dumps({'artifact': str(arguments.out), 'current_status': current['request'],
                  'current_claims': current['saved_claims'], 'current': current['snapshots'],
                  'baseline_matched': [item for item in baseline['snapshots'] if item['through_work_id'] == current['max_work_id']]}, ensure_ascii=False))
