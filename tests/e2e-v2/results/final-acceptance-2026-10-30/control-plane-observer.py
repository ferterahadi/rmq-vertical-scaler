import subprocess,json,time,datetime
from pathlib import Path
out=Path('/Users/oddle/.codex/worktrees/v23-coverage-closure/rmq-vertical-scaler/tests/e2e-v2/evidence/final-acceptance-2026-10-30');cmd=['kubectl','--kubeconfig','/private/tmp/rmq-v23-closure-kubeconfig','--context','kind-rmq-v23-closure','--request-timeout=10s'];ns='rmq-v23-e2e-native';(out/'control-plane-observer.py').write_bytes(Path(__file__).read_bytes())
def get(*args):
 p=subprocess.run(cmd+list(args),capture_output=True,text=True,timeout=20)
 return json.loads(p.stdout) if not p.returncode else {'error':p.stderr.strip()}
while True:
 progress=json.loads((out/'suite-progress.json').read_text());group=progress['runs'][-1]['name'] if progress['runs'] else 'setup';pods=get('get','pods','-n',ns,'-l','app.kubernetes.io/name=rmq-probe','-o','json');cm=get('get','configmap','rmq-vertical-scaler-status','-n',ns,'-o','json');record={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'group':group,'pods':[]}
 for p in pods.get('items',[]):
  record['pods'].append({'name':p['metadata']['name'],'uid':p['metadata']['uid'],'createdAt':p['metadata']['creationTimestamp'],'deletionTimestamp':p['metadata'].get('deletionTimestamp'),'ip':p.get('status',{}).get('podIP'),'desired':[{'name':c['name'],'resources':c.get('resources')} for c in p['spec']['containers']],'conditions':p.get('status',{}).get('conditions'),'containers':p.get('status',{}).get('containerStatuses'),'initContainers':p.get('status',{}).get('initContainerStatuses')})
 if 'error' in pods:record['error']=pods['error']
 if cm.get('data',{}).get('status.json'):
  state=json.loads(cm['data']['status.json']);record.update(statusAt=state['at'],phase=state['phase'],reason=state.get('reason'),revision=state['revision'],policyHash=state['policyHash'],operation=state.get('operation'),lastOperation=state.get('lastOperation'))
 with (out/'control-plane-observations.jsonl').open('a') as file:file.write(json.dumps(record)+'\n')
 if progress.get('passed') or any(r.get('exitCode',0)!=0 for r in progress['runs']):break
 time.sleep(3)
print('Independent control-plane observer complete',flush=True)
