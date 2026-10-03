import { useEffect, useRef, useState, type KeyboardEvent } from 'react';

// Same bound the engine enforces (control.MaxNameRunes); the engine still validates.
export const MAX_NAME = 64;

type Props = {
  value: string;
  placeholder: string;
  className?: string;
  as?: 'h1' | 'span';
  onRename(name: string): Promise<void>;
};

// EditableName shows a bridge label that turns into a text field on double
// click or F2. Enter or blur saves, Escape cancels; the new label shows at once
// and reverts if the engine rejects it.
export default function EditableName({ value, placeholder, className = '', as: Tag = 'span', onRename }: Props) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);
  const [pending, setPending] = useState<string | null>(null);
  const input = useRef<HTMLInputElement>(null);
  const label = useRef<HTMLElement>(null);
  const done = useRef(false);
  // Any new value from the engine settles the optimistic label, including a
  // reset that falls back to the project name.
  useEffect(() => { setPending(null); }, [value]);
  useEffect(() => { if (editing) { input.current?.focus(); input.current?.select(); } }, [editing]);
  const shown = pending ?? value;
  function start() { done.current = false; setDraft(shown); setEditing(true); }
  function finish(save: boolean) {
    if (done.current) return;
    done.current = true;
    setEditing(false);
    requestAnimationFrame(() => label.current?.focus());
    const name = draft.trim();
    if (!save || name === shown) return;
    // A cleared name falls back to the project, which only the engine knows.
    if (name) setPending(name);
    onRename(name).catch(() => setPending(null));
  }
  function key(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') { e.preventDefault(); finish(true); }
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); finish(false); }
  }
  if (editing) return <input ref={input} className={`name-input ${className}`} aria-label="Nombre del puente" value={draft} maxLength={MAX_NAME} placeholder={placeholder} onChange={e => setDraft(e.target.value)} onKeyDown={key} onBlur={() => finish(true)} />;
  return <Tag ref={label as never} className={`editable-name ${className}`} tabIndex={0} title="Doble clic o F2 para renombrar" aria-label={`${shown || placeholder}. Doble clic o F2 para renombrar`} onDoubleClick={start} onKeyDown={e => { if (e.key === 'F2') { e.preventDefault(); start(); } }}>{shown || placeholder}</Tag>;
}
