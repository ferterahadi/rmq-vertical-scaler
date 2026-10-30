import datetime as dt,json,subprocess,time
from pathlib import Path
from urllib.parse import urlparse
out=Path('/Users/oddle/Documents/rmq-vertical-scaler/tests/e2e-v2/evidence/extras-2026-10-26')
command=['kubectl','--kubeconfig','/private/tmp/rmq-v23-kubeconfig','--context','kind-rmq-v23','--request-timeout=10s']
namespace='rmq-v23-e2e-native'
def get(*args):
 return json.loads(subprocess.check_output(command+list(args)+['-o','json'],text=True,timeout=20))
def now(): return dt.datetime.now(dt.timezone.utc).isoformat()
config=get('config','view','--minify')
assert urlparse(config['clusters'][0]['cluster']['server']).hostname in ('127.0.0.1','localhost','::1')
assert get('get','namespace',namespace)['metadata']['labels']['rmq-vertical-scaler-test']=='v23-feasibility'
sts=get('get','statefulset','rmq-probe-server','-n',namespace);owner=sts['metadata']['uid']
(out/'availability-monitor.py').write_bytes(Path(__file__).read_bytes())
records=[];end=time.monotonic()+1500
try:
 while True:
  pods=get('get','pods','-n',namespace)['items']
  owned=[p for p in pods if any(o.get('controller') and o['uid']==owner for o in p['metadata'].get('ownerReferences',[]))]
  ready=sum(not p['metadata'].get('deletionTimestamp') and any(s['name']=='rabbitmq' and s.get('ready') for s in p.get('status',{}).get('containerStatuses',[])) for p in owned)
  record={'time':now(),'readyNonTerminating':ready,'owned':len(owned)};records.append(record)
  with (out/'continuous-availability.jsonl').open('a') as f:f.write(json.dumps(record)+'\n')
  if ready<2: raise RuntimeError('fewer than two ready nonterminating brokers')
  if (out/'matrix-error.json').exists():raise RuntimeError('main matrix failed')
  if (out/'matrix-result.json').exists():break
  if time.monotonic()>end:raise RuntimeError('monitor timed out before main matrix completed')
  time.sleep(2)
 result={'passed':True,'startedAt':records[0]['time'],'endedAt':records[-1]['time'],'samples':len(records),'minimumReady':min(r['readyNonTerminating'] for r in records),'intervalSeconds':2}
except Exception as e:
 result={'passed':False,'time':now(),'error':str(e),'samples':len(records)}
(out/'continuous-availability-result.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result),flush=True)
raise SystemExit(0 if result['passed'] else 1)
