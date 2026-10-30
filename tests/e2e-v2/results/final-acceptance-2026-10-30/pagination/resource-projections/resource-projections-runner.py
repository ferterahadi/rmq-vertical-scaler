import sys,json,hashlib,math
from pathlib import Path
from types import SimpleNamespace
ROOT=Path('/Users/oddle/.codex/worktrees/v23-coverage-closure/rmq-vertical-scaler');sys.path.insert(0,str(ROOT/'tests/e2e-v2'))
from matrix import Matrix
from run import probe_module,utc
PARENT=ROOT/'tests/e2e-v2/evidence/final-acceptance-2026-10-30';OUT=PARENT/'pagination/resource-projections';frozen=json.loads((PARENT/'frozen-source-sha256.json').read_text());assert all(hashlib.sha256((ROOT/p).read_bytes()).hexdigest()==h for p,h in frozen.items());progress=json.loads((PARENT/'suite-progress.json').read_text());assert progress['passed'];assert not OUT.exists()
args=SimpleNamespace(kubeconfig='/private/tmp/rmq-v23-closure-kubeconfig',context='kind-rmq-v23-closure',image='docker.io/library/rmq-vertical-scaler@'+progress['imageDigest'],expected_image_id=progress['imageDigest'],output=str(OUT),scenario='native',load_seconds=60,down_seconds=60,cooldown_seconds=600,settle_seconds=60,reuse=True,extras_only=False)
m=Matrix(args);(OUT/'resource-projections-runner.py').write_bytes(Path(__file__).read_bytes());results=[]
try:
 m.probe.guard();assert not m.status().get('operation');before=m.current_pods();cr=m.probe.get('rabbitmqcluster','rmq-probe')['spec']['resources'];strategy=m.probe.get('statefulset','rmq-probe-server')['spec']['updateStrategy']
 for name,types,queue in [('cpu-only',['cpu'],False),('memory-only',['memory'],False),('paired-resources',['cpu','memory'],False),('combined',['cpu','memory'],True)]:
  policy=m.base();policy['updatePolicy']['mode']='Off';policy['profiles']['HIGH']['requests']['memory']='512Mi';resource=[{'type':typ,'metricType':'Utilization','metadata':{'value':'0.1' if typ=='cpu' else '1'}} for typ in types]
  if queue:policy['triggers']+=resource
  else:policy['triggers']=resource;policy.pop('rmq')
  m.install(policy,name)
  m.probe.wait('real resource demand selects HIGH without writes',lambda:m.observe()['decision']['recommendedProfileIndex']==1 and all(s['complete'] for s in m.status()['observation']['signals']),timeout=120)
  state=m.status();obs=state['observation'];samples={s['pod']:s for s in obs['samples']};independent={}
  for signal in obs['signals']:
   typ=signal['type']
   if typ not in ('cpu','memory'):continue
   usage='cpuCores' if typ=='cpu' else 'memoryBytes';values=[100*samples[b['name']][usage]/float(probe_module.quantity(b['effective']['requests'][typ])) for b in obs['brokers']];expected=max(values);assert math.isclose(signal['value'],expected,rel_tol=1e-9,abs_tol=1e-6);assert signal['profile']==1;independent[typ]=expected
  m.hold(15,lambda:m.resources_unchanged(before) and m.probe.get('rabbitmqcluster','rmq-probe')['spec']['resources']==cr and m.probe.get('statefulset','rmq-probe-server')['spec']['updateStrategy']==strategy,'Off resource policy changed broker/strategy state')
  role=m.probe.get('role','rmq-vertical-scaler-role');assert not any('pods/resize' in rule.get('resources',[]) for rule in role['rules']);m.save(name+'-state.json',state);results.append({'case':name,'passed':True,'busiestUtilizationPercent':independent,'recommendedProfileIndex':1,'brokerResourcesAndStrategyUnchanged':True,'boundary':'Low test targets use actual measured usage to exercise positive recommendations in Off; not an Auto pressure load test.'})
 m.save('resource-projections-result.json',{'passed':True,'cases':results,'imageDigest':progress['imageDigest'],'actualLiveMetrics':True,'pairedProfiles':True});print('All four live resource projection cases passed',flush=True)
finally:m.close()
