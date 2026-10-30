import json,subprocess,time,datetime
from pathlib import Path
OUT=Path('/Users/oddle/.codex/worktrees/v23-coverage-closure/rmq-vertical-scaler/tests/e2e-v2/evidence/final-acceptance-2026-10-30');NS='rmq-v23-e2e-native';CMD=['kubectl','--kubeconfig','/private/tmp/rmq-v23-closure-kubeconfig','--context','kind-rmq-v23-closure','--request-timeout=10s'];last={};(OUT/'cleanup-recheck-observer.py').write_bytes(Path(__file__).read_bytes())
while True:
 progress=json.loads((OUT/'suite-progress.json').read_text())
 if progress.get('passed') or any(r.get('exitCode',0)!=0 for r in progress['runs']):break
 result=subprocess.run(CMD+['get','namespace',NS,'-o','json'],capture_output=True,text=True,timeout=20)
 if not result.returncode:
  namespace=json.loads(result.stdout);metadata=namespace['metadata'];labels=metadata.get('labels',{});deleted=metadata.get('deletionTimestamp');conditions={c['type']:c for c in namespace.get('status',{}).get('conditions',[])};now=datetime.datetime.now(datetime.timezone.utc)
  expected=('NamespaceDeletionDiscoveryFailure','NamespaceDeletionContentFailure','NamespaceContentRemaining','NamespaceFinalizersRemaining')
  if (deleted and labels.get('rmq-v23-closure')==NS and labels.get('rmq-vertical-scaler-test')=='v23-feasibility' and
      (now-datetime.datetime.fromisoformat(deleted.replace('Z','+00:00'))).total_seconds()>15 and
      all(conditions.get(key,{}).get('status')=='False' for key in expected) and time.monotonic()-last.get(metadata['uid'],0)>30):
   patch={'metadata':{'uid':metadata['uid'],'resourceVersion':metadata['resourceVersion'],'annotations':{'rmq-v23-closure/cleanup-recheck':now.isoformat()}}}
   response=subprocess.run(CMD+['patch','namespace',NS,'--type=merge','-p',json.dumps(patch)],capture_output=True,text=True,timeout=20);last[metadata['uid']]=time.monotonic()
   record={'at':now.isoformat(),'namespaceUID':metadata['uid'],'deletionTimestamp':deleted,'conditions':conditions,'finalizersPreserved':namespace['spec'].get('finalizers'),'action':'request normal namespace-controller recheck by annotation only','exitCode':response.returncode,'error':response.stderr.strip()}
   with (OUT/'cleanup-rechecks.jsonl').open('a') as file:file.write(json.dumps(record)+'\n')
   print('Requested normal cleanup recheck',metadata['uid'],response.returncode,flush=True)
 time.sleep(5)
