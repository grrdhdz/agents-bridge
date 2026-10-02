import sys,os,json,time,subprocess
from pathlib import Path
raw=sys.stdin.buffer.read();start=time.monotonic()
try:
 r=subprocess.run(sys.argv[1:],input=raw,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=3)
 output=r.stdout;rc=r.returncode
except subprocess.TimeoutExpired:
 output=b'';rc=124
record={'time':time.time(),'elapsed_s':round(time.monotonic()-start,4),'input':json.loads(raw),'output':json.loads(output) if output.strip() else None,'returncode':rc}
# One atomic append per event; the log is evidence for this probe only.
f=os.open(os.environ['HOOK_EVIDENCE'],os.O_WRONLY|os.O_CREAT|os.O_APPEND,0o600)
os.write(f,(json.dumps(record,ensure_ascii=False)+'\n').encode());os.close(f)
sys.stdout.buffer.write(output)
