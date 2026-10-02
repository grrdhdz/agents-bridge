import type { Role } from '../api/types';
export type PreviewNote={role:Role;human:boolean;label:string};
export type BoardPreviews=Record<string,PreviewNote[]>;
export default function BoardPreview({notes=[]}:{notes?:PreviewNote[]}){
 const known=notes.slice(-4);
 return <div className="board-preview" aria-hidden="true"><div className="mini-connector"/>{(known.length?known:[null,null,null,null]).map((n,i)=><div key={i} className={`mini-note mini-note-${i} ${n?(n.human?'human-note':n.role):'empty-note'}`}>{n&&<strong>{n.label}</strong>}<span/><span/><span/></div>)}</div>;
}
