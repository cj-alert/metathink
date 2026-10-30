"""Read-only word counts from recorded chat-completion message content."""
import argparse,collections,json,re,sqlite3,statistics
from pathlib import Path

def words(text):
    return len(re.findall(r'\S+',text))

def stats(values):
    return {'n':len(values),'min':min(values),'median':statistics.median(values),'max':max(values)} if values else None

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--manifest',type=Path,required=True)
p.add_argument('--out',type=Path,required=True)
a=p.parse_args()
m=json.loads(a.manifest.read_text(encoding='utf-8-sig'))
result={'method':'Count whitespace-delimited units in decoded message content; system/developer instructions separated from other message content. HTTP metadata and replies excluded. All recorded drone Contribute/Expand chat-completion starts included, including failed provider attempts; nested Responses research excluded. No runtime changes or model calls.','runs':[]}
for run in m['runs']:
    folder=Path(run['run_folder'])
    config=json.loads((folder/'config.json').read_text(encoding='utf-8-sig'))
    db=sqlite3.connect((folder/config['files']['debug']).resolve().as_uri()+'?mode=ro',uri=True)
    rows=[]
    for log_id,scope,raw in db.execute("SELECT id,scope,details FROM hive_log WHERE event='llm.start' ORDER BY id"):
        d=json.loads(raw)
        phase=d.get('phase')
        request=d.get('request')
        if isinstance(request,str):request=json.loads(request)
        messages=(request or {}).get('messages')
        if scope!='drone' or phase not in ('contribute','expand') or not isinstance(messages,list):continue
        counts={'instructions':0,'context':0}
        for message in messages:
            content=message.get('content','')
            if not isinstance(content,str):raise ValueError('Unsupported non-text message at log '+str(log_id))
            counts['instructions' if message.get('role') in ('system','developer') else 'context']+=words(content)
        rows.append({'log_id':log_id,'phase':phase,**counts,'total':sum(counts.values())})
    db.close()
    item={'test':run['test'],'source_db':str(folder/config['files']['debug']),'calls':len(rows),'instruction_word_counts':dict(sorted(collections.Counter(r['instructions'] for r in rows).items())),'full_prompt_words':stats([r['total'] for r in rows]),'context_words':stats([r['context'] for r in rows]),'phases':{},'records':rows}
    for phase in ('contribute','expand'):
        selected=[r for r in rows if r['phase']==phase]
        item['phases'][phase]={'calls':len(selected),'instruction_word_counts':dict(sorted(collections.Counter(r['instructions'] for r in selected).items())),'full_prompt_words':stats([r['total'] for r in selected])}
    result['runs'].append(item)
with a.out.open('x',encoding='utf-8',newline='\n') as f:json.dump(result,f,indent=2);f.write('\n')
print(json.dumps({'artifact':str(a.out),'method':result['method'],'runs':[{k:v for k,v in r.items() if k!='records'} for r in result['runs']]}))
