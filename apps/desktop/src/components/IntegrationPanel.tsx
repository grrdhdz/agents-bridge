import { useState } from 'react';
import type { ApiClient } from '../api/client';
import type { IntegrationStatusResult, Harness } from '../api/types';
import Modal from './Modal';

export function hooksNeedReview(status?: IntegrationStatusResult): boolean {
  return !status || !status.cli_current || !!status.cli_error || Object.values(status.harnesses).some(h => !!h.error || (!h.opted_out && (!h.installed || !!h.trust_note)));
}
export default function IntegrationPanel({ client, status, error, onUpdate, onClose }: {
  client: ApiClient; status?: IntegrationStatusResult; error?: string;
  onUpdate(status: IntegrationStatusResult): void; onClose(): void;
}) {
  const [busy, setBusy] = useState<Harness>();
  const [changeError, setChangeError] = useState('');
  async function set(harness: Harness, enabled: boolean) {
    setBusy(harness); setChangeError('');
    try { onUpdate(await client.integrationSet({ harness, enabled })); }
    catch { setChangeError('No se pudo cambiar la integración. Se conserva el último estado conocido.'); }
    finally { setBusy(undefined); }
  }
  return <Modal title="Hooks de usuario" onCancel={onClose} busy={!!busy}>
    <div className="integration-panel">
      <p>Se mantienen al abrir la app, para todos tus proyectos. Solo actúan en sesiones vinculadas a agents-bridge.</p>
      {(error || changeError) && <p role="alert" className="integration-error">{changeError || error}</p>}
      {status && <>
        <dl className="integration-cli"><dt>CLI estable</dt><dd><code>{status.cli_path}</code></dd><dt>Versión</dt><dd>{status.cli_current ? 'Actualizada' : 'Revisar instalación'}</dd><dt>PATH de esta sesión</dt><dd>{status.cli_on_path ? 'Disponible' : 'Fuera del PATH'}</dd></dl>
        {status.cli_error && <p role="alert" className="integration-error">{status.cli_error}</p>}
        {status.path_note && <p>{status.path_note}</p>}
        {(['claude', 'codex'] as const).map(harness => {
          const name = harness === 'claude' ? 'Claude Code' : 'Codex'; const h = status.harnesses[harness];
          return <fieldset key={harness} disabled={!!busy}>
            <legend>{name}</legend>
            <label className="integration-toggle"><input type="checkbox" checked={!h.opted_out} onChange={e => void set(harness, e.target.checked)}/><span>Mantener hooks instalados — {name}</span></label>
            <p>{h.installed ? 'Instalados' : h.opted_out ? 'Desactivados por ti' : 'No instalados'}{busy === harness ? ' · Guardando…' : ''}</p>
            <code className="integration-path">{h.path}</code>
            {!h.opted_out && h.trust_note && <p>{h.trust_note}</p>}
            {h.error && <p role="alert" className="integration-error">{h.error}</p>}
          </fieldset>;
        })}
      </>}
    </div>
    <div className="dialog-actions"><button onClick={onClose} disabled={!!busy}>Cerrar panel de hooks</button></div>
  </Modal>;
}
