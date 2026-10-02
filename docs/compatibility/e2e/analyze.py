"""Read probe evidence and assert E1-E7 without launching any harness."""
import sys, json, statistics
from pathlib import Path
root=Path(sys.argv[1])
for harness in ['claude','codex']:
 d=root/harness
 hooks=[json.loads(l) for l in (d/'hooks.jsonl').read_text().splitlines()]
 events=[json.loads(l) for l in (d/'orchestrator.jsonl').read_text().splitlines()]
 summary=json.loads((d/'summary.json').read_text());cleanup=json.loads((d/'cleanup.json').read_text())
 def cmd(r):return r['input'].get('tool_input',{}).get('command','')
 def find(event,needle):return [(i+1,r) for i,r in enumerate(hooks) if r['input']['hook_event_name']==event and needle in cmd(r)]
 binding=next(e for e in events if e['event']=='bindings')
 e1=any(b['instance_id']==summary['instance_id'] and b['harness']==harness for b in binding['data']['bindings'])
 work=[find('PostToolUse','TRABAJO-'+str(i))[0] for i in range(1,9)]
 e2=all(work) and 'RESULTADO\nTRABAJO-OK' in summary['result']
 blocks=[(i+1,r) for i,r in enumerate(hooks) if (r['output'] or {}).get('decision')=='block']
 block=blocks[0]
 forced=next(e for e in events if e['event']=='stop_forced_wait')
 e3=summary['forced_wait'] and forced['data']['wait_time']>block[1]['time']
 injections=[(i+1,r) for i,r in enumerate(hooks) if summary['urgent_code'] in str(r['output'])]
 injection=injections[0];seen=find('PreToolUse','echo VISTO-'+summary['urgent_code'])[0]
 after_injection=[r for _,r in find('PreToolUse','ctl wait') if r['time']>injection[1]['time']]
 e4=len(injections)==1 and injection[1]['input']['hook_event_name']=='PostToolUse' and injection[1]['time']<seen[1]['time']<after_injection[0]['time']
 final_peek=next(e for e in events if e['event']=='final_executor_peek')
 final_stops=[(i+1,r) for i,r in enumerate(hooks) if r['input']['hook_event_name']=='Stop' and not r['output']]
 e5=summary['fin_sent'] and final_peek['data']['fin_received'] and bool(final_stops) and summary['returncode']==0 and not summary['timeout']
 work_start=find('PreToolUse','TRABAJO-1')[0][1]['time'];work_end=work[-1][1]['time']
 samples=[e['data'][0] for e in events if e['event']=='ps' and e['data'] and work_start<=e['time']<=work_end]
 e6=work_end-work_start>20 and bool(samples) and all(s['role_states']['executor']['hook_bound'] for s in samples) and all(s['idle_seconds']<20 for s in samples)
 denied=[(i+1,r) for i,r in enumerate(hooks) if (r['output'] or {}).get('hookSpecificOutput',{}).get('permissionDecision')=='deny']
 deny=denied[0];tool_id=deny[1]['input']['tool_use_id']
 e7='ENSAYO-GUARDA' in cmd(deny[1]) and not any(r['input']['hook_event_name']=='PostToolUse' and r['input'].get('tool_use_id')==tool_id for r in hooks) and not any('\nRESULTADO\nENSAYO-GUARDA' in e['data'] for e in events if e['event']=='orchestrator_received')
 results=dict(zip(['E1','E2','E3','E4','E5','E6','E7'],[e1,e2,e3,e4,e5,e6,e7]))
 report={'harness':harness,'results':results,'work_span_s':round(work_end-work_start,3),'max_idle_during_work_s':max(s['idle_seconds'] for s in samples),'hook_median_ms':round(statistics.median(r['elapsed_s'] for r in hooks)*1000,2),'hook_max_ms':round(max(r['elapsed_s'] for r in hooks)*1000,2),'hook_lines':{'first_wait':find('PreToolUse','ctl wait')[0][0],'urgent_injection':injection[0],'canary_echo':seen[0],'deny':deny[0],'result':find('PostToolUse','TRABAJO-OK')[0][0],'stop_block':block[0],'final_stop':final_stops[-1][0]},'cleanup':cleanup}
 (d/'analysis.json').write_text(json.dumps(report,indent=2)+'\n')
 print(json.dumps(report))
 assert all(results.values()),results
 assert cleanup['global_hashes_unchanged'] and cleanup['own_processes_exited'] and cleanup['temporary_codex_home_removed'] and cleanup['runtime_removed'],cleanup
