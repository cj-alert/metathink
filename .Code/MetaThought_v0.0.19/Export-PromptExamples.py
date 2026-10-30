"""Export actual recorded prompts, selected by phase and chromosome, without model calls."""
import argparse,json,re,sqlite3
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--manifest',type=Path,required=True)
p.add_argument('--output-directory',type=Path,required=True)
p.add_argument('--prefix',required=True)
p.add_argument('--tests',nargs='+',type=int,required=True)
p.add_argument('--phase',choices=['contribute','expand'],required=True)
p.add_argument('--chromosome',type=int,default=0)
a=p.parse_args()
m=json.loads(a.manifest.read_text(encoding='utf-8-sig'))
results=[]
for run in m['runs']:
    if run['test'] not in a.tests:continue
    folder=Path(run['run_folder'])
    config=json.loads((folder/'config.json').read_text(encoding='utf-8-sig'))
    needle=config['chromosomes'][a.chromosome][a.phase]
    db=sqlite3.connect((folder/config['files']['debug']).resolve().as_uri()+'?mode=ro',uri=True)
    selected=None
    for lid,ts,drone,model,raw in db.execute("SELECT id,ts,drone_id,model,details FROM hive_log WHERE event='llm.start' AND scope='drone' ORDER BY id"):
        d=json.loads(raw)
        if d.get('phase')!=a.phase:continue
        request=d.get('request')
        if isinstance(request,str):request=json.loads(request)
        messages=(request or {}).get('messages',[])
        instruction='\n\n'.join(x['content'] for x in messages if x.get('role') in ('developer','system'))
        if needle not in instruction:continue
        selected=(lid,ts,drone,model,request,instruction)
        break
    db.close()
    if selected is None:raise ValueError('No matching saved prompt for test '+str(run['test']))
    lid,ts,drone,model,request,instruction=selected
    messages=request['messages']
    counts=[{'role':x['role'],'words':len(re.findall(r'\S+',x['content']))} for x in messages]
    stem=a.prefix+'_Test'+str(run['test'])+'_Prompt_Example'
    exact=a.output_directory/(stem+'.json')
    readable=a.output_directory/(stem+'.md')
    metadata={'test':run['test'],'log_id':lid,'utc':ts,'drone_id':drone,'model':model,'phase':a.phase,'chromosome_index':a.chromosome,'source_db':str(folder/config['files']['debug']),'selection':'earliest saved matching drone call; independent real contexts, not synthetic matched inputs','word_counts':counts,'total_words':sum(x['words'] for x in counts)}
    with exact.open('x',encoding='utf-8',newline='\n') as f:json.dump({'source':metadata,'request':request},f,ensure_ascii=False,indent=2);f.write('\n')
    out=['# Test '+str(run['test'])+' — actual '+a.phase.title()+' prompt','',
         'Chromosome '+chr(65+a.chromosome)+'. Drone '+str(drone)+'. Saved llm.start log '+str(lid)+'. '+ts+'.',
         '',str(metadata['total_words'])+' words in the original message content: '+', '.join(str(c['words'])+' '+c['role']+' words' for c in counts)+'.','',
         'This is the first recorded '+a.phase.title()+' call using chromosome '+chr(65+a.chromosome)+' for this test. The instruction text below is verbatim. Context JSON is pretty-printed only; the exact decoded message strings are retained in the [JSON companion](<'+str(exact.resolve())+'>). No prompt was reconstructed from current templates and no model call was made.','']
    for x in messages:
        out+=['## '+x['role'].title()+' message','']
        text=x['content']
        if x['role']=='user':
            try:text=json.dumps(json.loads(text),ensure_ascii=False,indent=2)
            except ValueError:pass
            out+=['~~~~json',text,'~~~~','']
        else:out+=['~~~~text',text,'~~~~','']
    with readable.open('x',encoding='utf-8',newline='\n') as f:f.write('\n'.join(out))
    results.append({**metadata,'markdown':str(readable.resolve()),'exact_message_file':str(exact.resolve())})
print(json.dumps(results,ensure_ascii=False))
