import subprocess,json,time,datetime,hashlib
from pathlib import Path
from urllib.parse import urlparse
cmd=['kubectl','--kubeconfig','/private/tmp/rmq-v23-closure-kubeconfig','--context','kind-rmq-v23-closure','--request-timeout=10s'];ns='rmq-v23-e2e-native';out=Path('/Users/oddle/.codex/worktrees/v23-coverage-closure/rmq-vertical-scaler/tests/e2e-v2/evidence/closure-operator-repeat-2026-10-30')
def get(*args):return json.loads(subprocess.check_output(cmd+list(args)+['-o','json'],text=True,timeout=20))
assert urlparse(get('config','view','--minify')['clusters'][0]['cluster']['server']).hostname in ['127.0.0.1','localhost','::1']
assert get('get','namespace',ns)['metadata']['labels']['rmq-v23-closure']==ns
(out/'observer.py').write_bytes(Path(__file__).read_bytes());end=time.monotonic()+1500;count=0
while time.monotonic()<end:
 if (out/'closure-error.json').exists():break
 try:
  cr=get('get','rabbitmqcluster','rmq-probe','-n',ns);st=get('get','statefulset','rmq-probe-server','-n',ns);state=json.loads(get('get','configmap','rmq-vertical-scaler-status','-n',ns)['data']['status.json'])
  row={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'statusAt':state['at'],'reason':state.get('reason'),'operation':state.get('operation'),'clusterUID':cr['metadata']['uid'],'clusterResources':cr['spec']['resources'],'override':cr['spec'].get('override'),'templateResources':next(c['resources'] for c in st['spec']['template']['spec']['containers'] if c['name']=='rabbitmq'),'strategy':st['spec']['updateStrategy'],'templateAnnotations':st['spec']['template']['metadata'].get('annotations',{}),'statefulSetGeneration':st['metadata']['generation']}
  with (out/'control-plane-observations.jsonl').open('a') as file:file.write(json.dumps(row)+'\n')
  count+=1
 except Exception as error:
  with (out/'observer-errors.jsonl').open('a') as file:file.write(json.dumps({'time':datetime.datetime.now(datetime.timezone.utc).isoformat(),'error':str(error)})+'\n')
 result=out/'closure-result.json'
 if result.exists() and len(json.loads(result.read_text())['cases'])>=6:break
 time.sleep(5)
print('Control-plane snapshots retained:',count,flush=True)
