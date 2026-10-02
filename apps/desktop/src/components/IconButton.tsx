import type { ButtonHTMLAttributes } from 'react';
import type { LucideIcon } from 'lucide-react';
export default function IconButton({icon:Icon,label,tooltip=label,className='',...props}: {icon:LucideIcon;label:string;tooltip?:string}&ButtonHTMLAttributes<HTMLButtonElement>){
 return <button type="button" className={`icon-button ${className}`} aria-label={label} title={tooltip} data-tooltip={tooltip} {...props}><Icon size={19} strokeWidth={1.8} aria-hidden="true" focusable="false"/></button>;
}
