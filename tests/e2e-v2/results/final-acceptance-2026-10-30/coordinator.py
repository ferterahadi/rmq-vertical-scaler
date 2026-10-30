import subprocess,json,time,datetime,hashlib,importlib.util,sys,shutil
from pathlib import Path
ROOT=Path('/Users/oddle/.codex/worktrees/v23-coverage-closure/rmq-vertical-scaler');OUT=ROOT/'tests/e2e-v2/evidence/final-acceptance-2026-10-30';PYTHON='/private/tmp/rmq-v23-closure-python/bin/python';DIGEST='sha256:5bec6039f6cd43dc829f2141007a113af5487fa61b6782fb824a905bca41a2b9';IMAGE='docker.io/library/rmq-vertical-scaler@'+DIGEST;KUBE=['--kubeconfig','/private/tmp/rmq-v23-closure-kubeconfig','--context','kind-rmq-v23-closure'];CMD=['kubectl']+KUBE;NS='rmq-v23-e2e-native'
spec=importlib.util.spec_from_file_location('gate',ROOT/'scripts/check-v23-release-evidence.py');gate=importlib.util.module_from_spec(spec);spec.loader.exec_module(gate)
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def hashes():return {p:hashlib.sha256((ROOT/p).read_bytes()).hexdigest() for p in sorted(gate.canonical_sources(ROOT))}
def kube(*args):return subprocess.run(CMD+list(args),check=True,text=True,capture_output=True).stdout
def save(name,value):p=OUT/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(json.dumps(value,indent=2)+'\n')
frozen=hashes();save('frozen-source-sha256.json',frozen);shutil.copyfile(__file__,OUT/'coordinator.py');progress={'startedAt':now(),'imageDigest':DIGEST,'runs':[]};save('suite-progress.json',progress)
config=json.loads(kube('config','view','--minify','-o','json'));assert config['current-context']=='kind-rmq-v23-closure' and config['clusters'][0]['cluster']['server'].startswith('https://127.0.0.1:')
# Remove only the prior diagnostic fixture after all its messages were accounted.
ns=json.loads(kube('get','namespace',NS,'-o','json'));assert ns['metadata']['labels'].get('rmq-v23-closure')==NS
kube('delete','deployment','rmq-vertical-scaler','-n',NS,'--ignore-not-found','--wait=false');kube('delete','rabbitmqcluster','rmq-probe','-n',NS,'--ignore-not-found','--wait=false')
end=time.monotonic()+300
while time.monotonic()<end:
 p=subprocess.run(CMD+['get','rabbitmqcluster','rmq-probe','-n',NS],capture_output=True,text=True)
 if p.returncode and 'NotFound' in p.stderr:break
 time.sleep(2)
else:raise RuntimeError('diagnostic cluster did not finalize')
kube('delete','namespace',NS,'--wait=true','--timeout=180s')
common=KUBE+['--image',IMAGE,'--expected-image-id',DIGEST]
def run(name,script,args):
 if hashes()!=frozen:raise RuntimeError('canonical sources changed after freeze')
 folder=OUT/name
 if folder.exists():raise FileExistsError('fresh output required: '+name)
 command=[PYTHON,str(ROOT/'tests/e2e-v2'/script)]+common+args+['--output',str(folder)];record={'name':name,'startedAt':now(),'command':command};progress['runs'].append(record);save('suite-progress.json',progress);print('START',name,now(),flush=True)
 with (OUT/(name+'.log')).open('w') as log:
  proc=subprocess.Popen(command,cwd=ROOT,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
  for line in proc.stdout:log.write(line);log.flush();print(name+': '+line,end='',flush=True)
  record.update(exitCode=proc.wait(),finishedAt=now());save('suite-progress.json',progress)
 if record['exitCode']!=0:raise RuntimeError(name+' failed; retained output must be inspected')
 if hashes()!=frozen:raise RuntimeError('canonical sources changed during '+name)
 print('PASS',name,now(),flush=True)
run('native','run.py',['--scenario','native','--startup-delay-seconds','0','--readiness-period-seconds','2'])
run('recovery','matrix.py',[])
run('deferred','matrix.py',['--deferred-only'])
# Observe actual headless-name DNS answers while the final rolling cycle runs.
dns_script='for i in $(seq 1 1800); do date -u +%Y-%m-%dT%H:%M:%SZ; for n in 0 1 2; do getent ahostsv4 rmq-probe-server-$n.rmq-probe-nodes.rmq-v23-e2e-native.svc.cluster.local || true; done; sleep 2; done'
dns={'apiVersion':'v1','kind':'Pod','metadata':{'name':'v23-final-dns-observer','namespace':NS,'labels':{'v23-final-observer':'true'}},'spec':{'automountServiceAccountToken':False,'containers':[{'name':'dns','image':'rabbitmq:4.4.0-management','command':['/bin/bash','-c',dns_script],'resources':{'requests':{'cpu':'5m','memory':'32Mi'},'limits':{'cpu':'100m','memory':'64Mi'}}}]}}
save('dns-observer.json',dns);kube('create','-f',str(OUT/'dns-observer.json'))
save('rolling-headless-service.json',json.loads(kube('get','service','rmq-probe-nodes','-n',NS,'-o','json')));save('rolling-dns-config.json',json.loads(kube('get','configmap','coredns','-n','kube-system','-o','json')))
run('paired_and_rolling','matrix.py',['--extras-only'])
(OUT/'rolling-dns-observer.log').write_text(kube('logs','v23-final-dns-observer','-n',NS));save('rolling-endpoint-slices.json',json.loads(kube('get','endpointslices','-n',NS,'-l','kubernetes.io/service-name=rmq-probe-nodes','-o','json')))
run('scope','scope.py',[])
# Guard label is set only on the namespace already verified as our native fixture.
ns=json.loads(kube('get','namespace',NS,'-o','json'));assert ns['metadata']['labels'].get('rmq-vertical-scaler-test')=='v23-feasibility';kube('label','namespace',NS,'rmq-v23-closure='+NS,'--overwrite')
run('platform','closure.py',['--group','platform'])
run('operator','closure.py',['--group','operator'])
observer_source=Path('/private/tmp/rmq-v23-live-metrics-repeat.py').read_text();old='/Users/oddle/.codex/worktrees/v23-coverage-closure/rmq-vertical-scaler/tests/e2e-v2/evidence/closure-metrics-repeat-2026-10-30';observer_source=observer_source.replace(old,str(OUT/'pagination'));needle="state=json.loads(cm['data']['status.json']);samples=";replacement="state=json.loads(cm['data']['status.json']);signals=state.get('observation',{}).get('signals',[])\n  if len(signals)!=2 or [s.get('value') for s in signals]!=[37,20] or not all(s.get('complete') for s in signals):\n   time.sleep(3);continue\n  samples=";assert needle in observer_source;observer_source=observer_source.replace(needle,replacement);observer_source=observer_source.replace('out.mkdir(exist_ok=True);', 'while not out.exists():time.sleep(1)\n');compile(observer_source,'metrics-observer.py','exec');observer=OUT/'metrics-observer.py';observer.write_text(observer_source)
collector=subprocess.Popen([PYTHON,str(observer)],cwd=ROOT,stdout=(OUT/'metrics-observer.log').open('w'),stderr=subprocess.STDOUT)
try:
 # The observer creates the directory; Closure requires an empty fresh directory.
 # Start the test first: observer waits until its folder exists instead of creating it.
 run('pagination','closure.py',['--group','metrics'])
 code=collector.wait(timeout=90)
 if code!=0:raise RuntimeError('live provider mapping observer failed')
 measured=json.loads((OUT/'pagination/live-metrics-collector.json').read_text());assert measured['passed'] and len(measured['matchedBrokers'])==3
finally:
 if collector.poll() is None:collector.terminate();collector.wait(timeout=10)
progress.update(passed=True,finishedAt=now(),fullAcceptanceMatrix=False,note='Runtime groups complete; final independent validation/local checks/review and report assembly remain.');save('suite-progress.json',progress);print('ALL RUNTIME GROUPS PASSED',flush=True)
