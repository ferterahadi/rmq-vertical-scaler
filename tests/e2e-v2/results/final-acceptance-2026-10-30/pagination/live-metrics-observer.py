import subprocess,json,time,datetime
from pathlib import Path
from decimal import Decimal
out=Path('/Users/oddle/.codex/worktrees/v23-coverage-closure/rmq-vertical-scaler/tests/e2e-v2/evidence/final-acceptance-2026-10-30/pagination');cmd=['kubectl','--kubeconfig','/private/tmp/rmq-v23-closure-kubeconfig','--context','kind-rmq-v23-closure','--request-timeout=10s'];ns='rmq-v23-e2e-native';names={f'rmq-probe-server-{i}' for i in range(3)}
def read(args):
 p=subprocess.run(cmd+args,text=True,capture_output=True,timeout=20)
 return json.loads(p.stdout) if p.returncode==0 else None
def quantity(text):
 for suffix,factor in [('Ki',1024),('Mi',1024**2),('Gi',1024**3),('n',Decimal('0.000000001')),('u',Decimal('0.000001')),('m',Decimal('0.001'))]:
  if text.endswith(suffix):return Decimal(text[:-len(suffix)])*factor
 return Decimal(text)
while not out.exists():time.sleep(1)
(out/'live-metrics-observer.py').write_bytes(Path(__file__).read_bytes());end=time.monotonic()+600;matched={};captures=[]
while time.monotonic()<end:
 if (out/"closure-error.json").exists():break
 raw=read(['get','--raw',f'/apis/metrics.k8s.io/v1beta1/namespaces/{ns}/pods']);cm=read(['get','configmap','rmq-vertical-scaler-status','-n',ns,'-o','json']);pods=read(['get','pods','-n',ns,'-l','app.kubernetes.io/name=rmq-probe','-o','json'])
 if raw and pods and len(pods.get('items',[]))==3 and cm:
  state=json.loads(cm['data']['status.json']);signals=state.get('observation',{}).get('signals',[])
  if len(signals)!=2 or [s.get('value') for s in signals]!=[37,20] or not all(s.get('complete') for s in signals):
   time.sleep(3);continue
  samples={s['pod']:s for s in state.get('observation',{}).get('samples',[])};census={p['metadata']['name']:p for p in pods['items']}
  for item in raw.get('items',[]):
   name=item['metadata']['name']
   if name not in names or name not in samples:continue
   sample=samples[name];timestamp=item['timestamp'];window=item['window'];container=next(c for c in item['containers'] if c['name']=='rabbitmq');status=next(c for c in census[name]['status']['containerStatuses'] if c['name']=='rabbitmq')
   if datetime.datetime.fromisoformat(timestamp.replace('Z','+00:00'))!=datetime.datetime.fromisoformat(sample['timestamp'].replace('Z','+00:00')):continue
   window_ns=int(Decimal(window[:-1])*1000000000);t=datetime.datetime.fromisoformat(timestamp.replace('Z','+00:00'));started=datetime.datetime.fromisoformat(status['state']['running']['startedAt'].replace('Z','+00:00'));now=datetime.datetime.now(datetime.timezone.utc)
   assert window_ns>0 and -1<=(now-t).total_seconds()<=60 and t-datetime.timedelta(seconds=float(Decimal(window[:-1])))>=started
   assert sample['uid']==census[name]['metadata']['uid'] and sample['containerID']==status['containerID'] and sample['window']==window_ns
   assert abs(Decimal(str(sample['cpuCores']))-quantity(container['usage']['cpu']))<Decimal('0.000000001')
   assert Decimal(str(sample['memoryBytes']))==quantity(container['usage']['memory'])
   matched[name]={'timestamp':timestamp,'window':window,'uid':sample['uid'],'containerID':sample['containerID'],'cpuCores':sample['cpuCores'],'memoryBytes':sample['memoryBytes']}
  captures.append({'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'provider':raw,'collectorSamples':list(samples.values()),'census':[{'name':p['metadata']['name'],'uid':p['metadata']['uid'],'containers':p['status']['containerStatuses']} for p in pods['items']]})
 if set(matched)==names:break
 time.sleep(3)
result={'passed':set(matched)==names,'matchedBrokers':matched,'captures':captures,'boundary':'Live Metrics API/collector value and lifecycle mapping; controlled tests separately exercise stale/invalid sources.'};(out/'live-metrics-collector.json').write_text(json.dumps(result,indent=2)+'\n');print('Live provider/collector matches:',len(matched),flush=True)
raise SystemExit(0 if result['passed'] else 1)
