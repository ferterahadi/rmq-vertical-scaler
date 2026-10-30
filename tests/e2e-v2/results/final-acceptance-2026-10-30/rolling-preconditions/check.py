from pathlib import Path
import json,time,sys,datetime
from types import SimpleNamespace
ROOT=Path('/Users/oddle/.codex/worktrees/v23-coverage-closure/rmq-vertical-scaler');OUT=ROOT/'tests/e2e-v2/evidence/final-acceptance-2026-10-30';sys.path.insert(0,str(ROOT/'tests/e2e-v2'))
from run import Cycle,utc
while True:
 progress=json.loads((OUT/'suite-progress.json').read_text())
 if any(r['name']=='paired_and_rolling' for r in progress['runs']):break
 if progress.get('passed') or any(r.get('exitCode',0)!=0 for r in progress['runs']):raise RuntimeError('suite stopped before rolling')
 time.sleep(3)
args=SimpleNamespace(kubeconfig='/private/tmp/rmq-v23-closure-kubeconfig',context='kind-rmq-v23-closure',output=str(OUT/'rolling-preconditions'),scenario='native');cycle=Cycle(args)
try:
 cycle.high={'requests':{'cpu':'800m','memory':'512Mi'},'limits':{'cpu':'1500m','memory':'1536Mi'}};capacity=cycle.capacity();capacity['largestProfile']=cycle.high;assert capacity['enoughRequestedResources'];cycle.save('capacity.json',capacity);cycle.save('source-metrics.json',cycle.probe.kubectl('get','--raw','/apis/metrics.k8s.io/v1beta1/namespaces/rmq-v23-e2e-native/pods',as_json=True));print('Largest paired/rolling profile fits declared healthy node capacity',flush=True)
finally:cycle.close()
