import type { Role } from '../api/types';
export default function RoleAvatar({role,connected=true}:{role:Role;connected?:boolean}){
 return <span className={`role-avatar ${role}`} aria-hidden="true">{role==='orchestrator'?'O':'E'}<i className={connected?'avatar-dot connected':'avatar-dot'}/></span>;
}
