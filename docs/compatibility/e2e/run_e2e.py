import os,sys,json,time,signal,subprocess,hashlib,shlex,tempfile,shutil,uuid
from pathlib import Path
P=Path(os.environ.get("AGENTS_BRIDGE_PROBE_DIR",str(Path(__file__).resolve().parent))).resolve();BIN=P/'bin/agents-bridge';H=sys.argv[1];D=P/H
if D.exists():raise SystemExit('Archive the existing '+str(D)+' before repeating the probe')
D.mkdir()
PROJECT=D/'project';PROJECT.mkdir(exist_ok=True);RUNTIME=D/'runtime';RUNTIME.mkdir(exist_ok=True);RUNTIME.chmod(0o700)
tracked=[Path.home()/'.claude/settings.json',Path.home()/'.codex/hooks.json',Path.home()/'.codex/config.toml']
def hashes():return {str(p):hashlib.sha256(p.read_bytes()).hexdigest() if p.exists() else None for p in tracked}
(D/'global-before.json').write_text(json.dumps(hashes(),indent=2))
env=os.environ.copy()
for k in list(env):
 if k.startswith('HERDR_') or k=='CLAUDECODE':env.pop(k,None)
env['TMPDIR']=str(RUNTIME);env['PATH']=str(BIN.parent)+os.pathsep+env['PATH'];env['HOOK_EVIDENCE']=str(D/'hooks.jsonl')
(D/'hooks.jsonl').write_text('')
procs=[];codexhome=None;cfg=None;integration_removed=False

def run(args,body=None):
 r=subprocess.run([str(BIN)]+args,input=body,text=True,cwd=PROJECT,env=env,capture_output=True,timeout=8)
 if r.returncode:raise RuntimeError(f'{args[0]} failed {r.returncode}: {r.stderr[:300]}')
 return r.stdout

def log(name,value):
 with (D/'orchestrator.jsonl').open('a') as f:f.write(json.dumps({'time':time.time(),'event':name,'data':value})+'\n')
def hookrecords():
 records=[]
 for line in (D/'hooks.jsonl').read_text().splitlines():
  try:records.append(json.loads(line))
  except json.JSONDecodeError:pass
 return records

def killown(proc):
 if proc.poll() is not None: return
 try:os.killpg(proc.pid,signal.SIGTERM)
 except ProcessLookupError:pass
 except PermissionError:proc.terminate()
 try:proc.wait(timeout=5)
 except subprocess.TimeoutExpired:
  try:os.killpg(proc.pid,signal.SIGKILL)
  except ProcessLookupError:pass
  proc.wait(timeout=5)

try:
 if H=='codex':
  codexhome=Path(tempfile.mkdtemp(prefix='codex-home-',dir=D));codexhome.chmod(0o700)
  shutil.copyfile(Path.home()/'.codex/auth.json',codexhome/'auth.json');(codexhome/'auth.json').chmod(0o600)
  (codexhome/'config.toml').write_text('model = "gpt-6.1-sol"\nmodel_reasoning_effort = "low"\n[features]\nhooks = true\n[projects.'+json.dumps(str(PROJECT))+']\ntrust_level = "trusted"\n')
  env['CODEX_HOME']=str(codexhome)
  (D/'trust.json').write_text(json.dumps({'scope':'temporary CODEX_HOME','project':str(PROJECT),'trust_level':'trusted','bypass_hook_trust':'per invocation; no hashes persisted'},indent=2))
 installed=run(['integration','install',H,'--scope','project','--project',str(PROJECT)])
 (D/'installed.json').write_text(installed)
 cfg=PROJECT/('.claude/settings.json' if H=='claude' else '.codex/hooks.json')
 (D/'installed-config.json').write_bytes(cfg.read_bytes())
 config=json.loads(cfg.read_text())
 for event,groups in config['hooks'].items():
  for g in groups:
   for h in g['hooks']:
    h['command']=shlex.quote(sys.executable)+' '+shlex.quote(str(P/'observe.py'))+' '+h['command']
 cfg.write_text(json.dumps(config,indent=2)+'\n')
 (D/'observed-config.json').write_bytes(cfg.read_bytes())
 ready=D/'ready.json';ready.unlink(missing_ok=True)
 bf=(D/'bridge.stdout').open('w');be=(D/'bridge.stderr').open('w')
 bridge=subprocess.Popen([str(BIN),'local','--headless','--idle-timeout','20s','--ready-file',str(ready)],cwd=PROJECT,env=env,stdout=bf,stderr=be,start_new_session=True);procs.append(bridge)
 deadline=time.monotonic()+10
 while not ready.exists():
  if bridge.poll() is not None or time.monotonic()>deadline:raise RuntimeError('bridge did not become ready')
  time.sleep(.1)
 info=json.loads(ready.read_text());ID=info['instance_id'];log('ready',{'instance_id':ID,'pid':bridge.pid,'idle_timeout_s':20})
 def ctl(action,role='orchestrator',extra=None,body=None):return run(['ctl',action,'--instance-id',ID,'--role',role]+(extra or []),body)
 task='''TAREA
Valida el puente sin leer archivos ni usar otras herramientas que el shell. Ejecuta OCHO herramientas separadas, en orden, cada una con `echo TRABAJO-i; sleep 4` (i de 1 a 8). No combines estas ocho llamadas: comprobamos latidos durante más de 20 segundos.
Si PostToolUse te avisa de un URGENTE, el aviso contiene un código nuevo: demuéstralo inmediatamente con una herramienta `echo VISTO-<código>`, antes de cualquier ctl wait. Después intenta UNA vez enviar RESULTADO con cuerpo ENSAYO-GUARDA sin --force y antes de leer la bandeja, para comprobar PreToolUse: usa printf literal seguido de ctl send --body-file -. Debe denegarse. Entonces consume el URGENTE con ctl wait y continúa las herramientas restantes.
Después envía RESULTADO con TRABAJO-OK y el código que viste. Finalmente intenta terminar el turno respondiendo TERMINADO; no ejecutes otra espera por iniciativa propia después de RESULTADO (estamos probando Stop). Si un hook bloquea Stop, sigue lo que indique. No cierres el puente.
'''
 log('task_sent',json.loads(ctl('send',extra=['--body-file','-'],body=task)))
 prompt=f'usa agents-bridge como ejecutor con --instance-id {ID}: ejecuta agents-bridge ctl wait --instance-id {ID} --role executor --timeout 5m --format text y haz lo que pida el mensaje. El binario de esta prueba está en {BIN}; usa exclusivamente este puente y el shell.'
 if H=='claude':
  cmd=['claude','-p',prompt,'--setting-sources','project','--no-session-persistence','--tools','Bash','--allowedTools','Bash','--strict-mcp-config','--mcp-config','{"mcpServers":{}}','--output-format','stream-json','--verbose','--include-hook-events']
 else:
  cmd=['codex','exec','--ignore-rules','--ephemeral','--skip-git-repo-check','--dangerously-bypass-approvals-and-sandbox','--dangerously-bypass-hook-trust','--enable','hooks','-c','project_root_markers=[".codex"]','-c','web_search="disabled"','--json',prompt]
 (D/'invocation.json').write_text(json.dumps({'cmd':cmd,'runtime':str(RUNTIME),'project':str(PROJECT),'versions':{h:subprocess.check_output([h,'--version'],text=True).strip() for h in ['claude','codex']}},indent=2))
 ef=(D/'executor.stdout.jsonl').open('w');ee=(D/'executor.stderr').open('w')
 start=time.monotonic();executor=subprocess.Popen(cmd,cwd=PROJECT,env=env,stdin=subprocess.DEVNULL,stdout=ef,stderr=ee,start_new_session=True);procs.append(executor);log('executor_started',{'pid':executor.pid,'pgid':executor.pid})
 urgent_sent=False;fin_sent=False;bindings_logged=False;second_wait=False;result=None;last_health=0;urgent_code='CANARIO-'+H.upper()+'-'+uuid.uuid4().hex[:6]
 while executor.poll() is None and time.monotonic()-start<240:
  records=hookrecords()
  if not bindings_logged and any(r['input'].get('hook_event_name')=='PreToolUse' and 'ctl wait' in r['input'].get('tool_input',{}).get('command','') for r in records):
   log('bindings',json.loads(run(['bind','--list'])));bindings_logged=True
  if not urgent_sent and any(r['input'].get('hook_event_name')=='PreToolUse' and 'TRABAJO-2' in r['input'].get('tool_input',{}).get('command','') for r in records):
   log('urgent_sent',{'code':urgent_code,'response':json.loads(ctl('send',extra=['--body-file','-'],body='URGENTE\n'+urgent_code+' aviso para el ensayo de PostToolUse.'))});urgent_sent=True
  blocks=[r for r in records if r.get('output',{} ) and r['output'].get('decision')=='block']
  if blocks and not fin_sent:
   block_time=blocks[0]['time']
   waits=[r for r in records if r['time']>block_time and r['input'].get('hook_event_name')=='PreToolUse' and 'ctl wait' in r['input'].get('tool_input',{}).get('command','')]
   if waits:
    log('stop_forced_wait',{'block_time':block_time,'wait_time':waits[0]['time']});second_wait=True
    log('fin_sent',json.loads(ctl('send',extra=['--body-file','-'],body='FIN\nEnsayo completado. Puedes terminar.')));fin_sent=True
  if time.monotonic()-last_health>=2:
   rows=json.loads(run(['ps','--format','jsonl']))['instances'];rows=[r for r in rows if r['instance_id']==ID];log('ps',rows);last_health=time.monotonic()
   peek=json.loads(ctl('peek'))
   if peek.get('unread',0)>0:
    message=ctl('wait',extra=['--timeout','1s','--format','text']);log('orchestrator_received',message)
    if 'TRABAJO-OK' in message:result=message
  time.sleep(.2)
 timedout=executor.poll() is None
 if timedout:killown(executor)
 ef.close();ee.close()
 log('executor_exit',{'code':executor.returncode,'timeout':timedout,'elapsed_s':round(time.monotonic()-start,2)})
 if bridge.poll() is None:
  log('final_executor_peek',json.loads(ctl('peek','executor')))
  log('stopped',json.loads(run(['stop','--instance-id',ID])))
  bridge.wait(timeout=10)
 (D/'summary.json').write_text(json.dumps({'harness':H,'instance_id':ID,'urgent_code':urgent_code,'urgent_sent':urgent_sent,'forced_wait':second_wait,'fin_sent':fin_sent,'result':result,'timeout':timedout,'returncode':executor.returncode,'elapsed_s':round(time.monotonic()-start,2)},indent=2))
 # Restore the exact installer configuration before exercising uninstall.
 cfg.write_bytes((D/'installed-config.json').read_bytes());log('uninstalled',json.loads(run(['integration','uninstall',H,'--scope','project','--project',str(PROJECT)])));integration_removed=True
finally:
 cleanup_errors=[]
 for proc in reversed(procs):
  try:killown(proc)
  except Exception as error:cleanup_errors.append(type(error).__name__)
 if not integration_removed and cfg is not None and (D/'installed-config.json').exists():
  try:
   cfg.write_bytes((D/'installed-config.json').read_bytes())
   log('uninstalled_after_error',json.loads(run(['integration','uninstall',H,'--scope','project','--project',str(PROJECT)])))
  except Exception as error:cleanup_errors.append(type(error).__name__)
 if codexhome is not None:shutil.rmtree(codexhome)
 # Ready files contain metadata only in local mode; remove runtime credentials.
 if RUNTIME.exists():shutil.rmtree(RUNTIME)
 (D/'global-after.json').write_text(json.dumps(hashes(),indent=2))
 same=json.loads((D/'global-before.json').read_text())==hashes()
 (D/'cleanup.json').write_text(json.dumps({'global_hashes_unchanged':same,'own_processes_exited':all(p.poll() is not None for p in procs),'temporary_codex_home_removed':codexhome is None or not codexhome.exists(),'runtime_removed':not RUNTIME.exists(),'cleanup_errors':cleanup_errors},indent=2))
 print(H,'cleanup complete; global hashes unchanged:',same,flush=True)
