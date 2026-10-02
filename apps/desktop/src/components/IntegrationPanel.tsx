import { useState } from 'react';
import { Bot, CircleAlert, Code2, Info, Plug, Terminal } from 'lucide-react';
import type { ApiClient } from '../api/client';
import type { IntegrationStatusResult, Harness } from '../api/types';
import IconButton from './IconButton';
import Modal from './Modal';

export function hooksNeedReview(status?: IntegrationStatusResult): boolean {
  return !status || !status.cli_current || !!status.cli_error || Object.values(status.harnesses).some(h => !!h.error || (!h.opted_out && (!h.installed || !!h.trust_note)));
}

type HarnessStatus = IntegrationStatusResult['harnesses'][Harness];
type StateKind = 'installed' | 'review' | 'disabled' | 'error';
function harnessState(h: HarnessStatus): { kind: StateKind; label: string } {
  if (h.error) return { kind: 'error', label: 'Error' };
  if (h.opted_out) return { kind: 'disabled', label: 'Desactivados' };
  if (!h.installed || h.trust_note) return { kind: 'review', label: 'Revisar' };
  return { kind: 'installed', label: 'Instalados' };
}
function StateBadge({ status }: { status: HarnessStatus }) {
  const { kind, label } = harnessState(status);
  return <span className={`hook-badge hook-badge-${kind}`}>{label}</span>;
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
  return <Modal title="Hooks de usuario" headerIcon={Plug} closeLabel="Cerrar panel de hooks" onCancel={onClose} busy={!!busy}>
    <div className="integration-panel">
      <p className="integration-intro">Hooks para todos tus proyectos. Solo actúan en sesiones vinculadas a agents-bridge.</p>
      {(error || changeError) && <div role="alert" className="integration-callout integration-callout-error"><CircleAlert size={16} aria-hidden="true"/><p>{changeError || error}</p></div>}
      {status && <>
        <section className="integration-section" aria-labelledby="integration-cli-heading">
          <h3 id="integration-cli-heading"><Terminal size={14} aria-hidden="true"/>CLI estable</h3>
          <dl className="integration-rows">
            <div><dt>Ruta</dt><dd><code>{status.cli_path}</code></dd></div>
            <div><dt>Versión</dt><dd>{status.cli_current ? 'Actualizada' : 'Revisar instalación'}</dd></div>
            <div><dt>PATH de esta sesión</dt><dd>{status.cli_on_path ? 'Disponible' : 'Fuera del PATH'}</dd></div>
          </dl>
          {status.cli_error && <div role="alert" className="integration-callout integration-callout-error"><CircleAlert size={16} aria-hidden="true"/><p>{status.cli_error}</p></div>}
          {status.path_note && <div className="integration-callout integration-callout-info"><Info size={16} aria-hidden="true"/><p>{status.path_note}</p></div>}
        </section>
        <section className="integration-section" aria-labelledby="integration-harness-heading">
          <h3 id="integration-harness-heading"><Plug size={14} aria-hidden="true"/>Harnesses</h3>
          {(['claude', 'codex'] as const).map(harness => {
            const name = harness === 'claude' ? 'Claude Code' : 'Codex';
            const h = status.harnesses[harness]; const Icon = harness === 'claude' ? Bot : Code2;
            const enabled = !h.opted_out;
            return <article className="integration-harness-card" key={harness}>
              <header className="integration-harness-heading">
                <span className="integration-avatar" aria-hidden="true"><Icon size={16}/></span>
                <div className="integration-harness-name"><h4 id={`hooks-harness-name-${harness}`}>{name}</h4><span>Hooks de usuario</span></div>
                <StateBadge status={h}/>
              </header>
              <dl className="integration-rows integration-harness-rows">
                <div><dt>Configuración</dt><dd><code>{h.path}</code></dd></div>
              </dl>
              <div className="integration-switch-row">
                <span id={`hooks-label-${harness}`}>Mantener hooks instalados — {name}</span>
                <button
                  type="button" role="switch" aria-checked={enabled}
                  aria-labelledby={`hooks-label-${harness}`} aria-describedby={`hooks-harness-${harness}`}
                  disabled={!!busy} className="integration-switch"
                  onClick={() => void set(harness, !enabled)}
                ><span className="integration-switch-thumb" aria-hidden="true"/></button>
              </div>
              <span className="integration-switch-help" id={`hooks-harness-${harness}`}>{busy === harness ? 'Guardando…' : enabled ? 'Se mantienen al abrir la app' : 'Desactivados por ti'}</span>
              {!h.opted_out && h.trust_note && <div className="integration-callout integration-callout-warning"><CircleAlert size={16} aria-hidden="true"/><p>{h.trust_note}</p></div>}
              {h.error && <div role="alert" className="integration-callout integration-callout-error"><CircleAlert size={16} aria-hidden="true"/><p>{h.error}</p></div>}
            </article>;
          })}
        </section>
      </>}
    </div>
  </Modal>;
}
