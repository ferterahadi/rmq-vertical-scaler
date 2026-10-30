import sys,json,time,datetime
from pathlib import Path
from types import SimpleNamespace
root=Path('/Users/oddle/.codex/worktrees/v23-coverage-closure/rmq-vertical-scaler');sys.path.insert(0,str(root/'tests/e2e-v2'))
from closure import Closure
from run import probe_module,utc
out=root/'tests/e2e-v2/evidence/rolling-default-diagnostic-2026-10-30'
args=SimpleNamespace(kubeconfig='/private/tmp/rmq-v23-closure-kubeconfig',context='kind-rmq-v23-closure',image='docker.io/library/rmq-vertical-scaler@sha256:04c17e5cd431ddbd5d03c7431985a28164f0f4b9e1483dc72e12943cb4df2e21',expected_image_id='sha256:04c17e5cd431ddbd5d03c7431985a28164f0f4b9e1483dc72e12943cb4df2e21',output=str(out),scenario='native',load_seconds=60,down_seconds=60,cooldown_seconds=600,settle_seconds=60,reuse=True)
m=Closure(args)
try:
 m.guard_disposable();m.fresh();policy=m.base();policy['profiles']['HIGH']={'requests':{'cpu':'800m','memory':'512Mi'},'limits':{'cpu':'1500m','memory':'1536Mi'}};policy['updatePolicy']['allowRollingFallback']=True;m.install(policy,'default-rolling');m.high=policy['profiles']['HIGH'];capacity=m.capacity();m.save('capacity.json',capacity);assert capacity['enoughRequestedResources'];m.save('startup-spec.json',{'cluster':m.probe.get('rabbitmqcluster','rmq-probe')['spec'],'template':m.probe.get('statefulset','rmq-probe-server')['spec']['template']['spec']});m.burst(100);end=time.monotonic()+360
 while time.monotonic()<end:
  s=m.observe();pods=m.probe.kubectl('get','pods','-n',m.namespace,'-l','app.kubernetes.io/name=rmq-probe','-o','json',as_json=True)['items'];record={'at':utc(),'statusAt':s['at'],'reason':s.get('reason'),'operation':s.get('operation'),'lastOperation':s.get('lastOperation'),'pods':[{'name':p['metadata']['name'],'uid':p['metadata']['uid'],'createdAt':p['metadata']['creationTimestamp'],'deleting':p['metadata'].get('deletionTimestamp'),'init':p.get('status',{}).get('initContainerStatuses'),'containers':p.get('status',{}).get('containerStatuses'),'conditions':p.get('status',{}).get('conditions')} for p in pods]}
  with (out/'pod-timeline.jsonl').open('a') as f:f.write(json.dumps(record)+'\n')
  m.rolling_availability()
  if not s.get('operation') and (s.get('lastOperation') or {}).get('direction')=='up':break
  time.sleep(2)
 else:raise RuntimeError('diagnostic rolling up never completed')
 op=s['lastOperation'];elapsed=(datetime.datetime.fromisoformat(op['completedAt'].replace('Z','+00:00'))-datetime.datetime.fromisoformat(op['episodeStart'].replace('Z','+00:00'))).total_seconds();m.save('up-operation.json',op);m.restart_forwards();m.probe.snapshot('increased',policy['profiles']['HIGH']);m.drain();assert m.confirmed==m.consumed;m.save('message-accounting.json',{'confirmed':sorted(m.confirmed),'consumed':sorted(m.consumed),'duplicates':m.duplicates});m.save('diagnostic-result.json',{'upCompletionSeconds':elapsed,'healthyUpObjectiveMet':elapsed<=120,'deadlineMissed':op['deadlineMissed'],'purpose':'Reproduce default Operator startup delay, not acceptance','confirmed':len(m.confirmed),'consumed':len(m.consumed)});print('DIAGNOSTIC',elapsed,flush=True)
except Exception as e:m.save('diagnostic-error.json',{'error':str(e)});raise
finally:m.close()
