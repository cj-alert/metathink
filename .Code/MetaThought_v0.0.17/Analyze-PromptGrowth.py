"""Measure saved prompt growth without changing hive state or making model calls."""
import argparse,json,re,sqlite3,statistics
from pathlib import Path

def wc(s):return len(re.findall(r'\S+',s))
def summary(rows):
    if not rows:return None
    values=[r['words'] for r in rows]
    return {'calls':len(rows),'min':min(values),'median':statistics.median(values),'max':max(values)}
def ro(path):return sqlite3.connect(path.resolve().as_uri()+'?mode=ro',uri=True)
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--manifest',type=Path,required=True)
p.add_argument('--out',type=Path,required=True)
a=p.parse_args()
m=json.loads(a.manifest.read_text(encoding='utf-8-sig'))
result={'method':'Read-only SQLite. Counts whitespace-separated words in actual message strings. Drone Contribute/Expand chat-completion starts, including provider failures, excluding nested Responses research. First/last50 are descriptive analysis windows, not runtime limits. Final population from saved state.','runs':[]}
for run in m['runs']:
    folder=Path(run['run_folder']);cfg=json.loads((folder/'config.json').read_text(encoding='utf-8-sig'))
    state=ro(folder/cfg['files']['state'])
    req=state.execute('SELECT MAX(id) FROM hive_requests').fetchone()[0]
    pop=state.execute('SELECT COUNT(*) FROM hive_worker_queue WHERE request_id=?',(req,)).fetchone()[0]
    inquiries=state.execute('SELECT COUNT(*) FROM hive_inquiries WHERE request_id=? AND proposed_by<>0',(req,)).fetchone()[0]
    state.close();db=ro(folder/cfg['files']['debug']);rows=[]
    for lid,ts,raw in db.execute("SELECT id,ts,details FROM hive_log WHERE event='llm.start' AND scope='drone' ORDER BY id"):
        d=json.loads(raw)
        if d.get('phase') not in ('contribute','expand'):continue
        q=d.get('request');q=json.loads(q) if isinstance(q,str) else q
        messages=(q or {}).get('messages')
        if not isinstance(messages,list):continue
        user=next((x['content'] for x in messages if x.get('role')=='user'),'{}')
        context=json.loads(user)
        rows.append({'log_id':lid,'utc':ts,'phase':d['phase'],'words':sum(wc(x['content']) for x in messages),'selection_call':context.get('needs_fit_choice') is True,'inquiry_cards':len(context.get('inquiry_signals',[])),'membership_count':len(context.get('memberships',[]))})
    db.close()
    result['runs'].append({'test':run['test'],'final_drones':pop,'final_questions':inquiries,'all_calls':summary(rows),'first50':summary(rows[:50]),'last50':summary(rows[-50:]),'normal_calls':summary([r for r in rows if not r['selection_call']]),'selection_calls':summary([r for r in rows if r['selection_call']]),'max_inquiry_cards':max((r['inquiry_cards'] for r in rows),default=0),'max_memberships':max((r['membership_count'] for r in rows),default=0),'records':rows})
with a.out.open('x',encoding='utf-8',newline='\n') as f:json.dump(result,f,indent=2);f.write('\n')
print(json.dumps({'artifact':str(a.out),'runs':[{k:v for k,v in r.items() if k!='records'} for r in result['runs']]}))
