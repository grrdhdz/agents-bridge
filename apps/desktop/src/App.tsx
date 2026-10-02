import { useEffect, useState } from 'react';
import { hello } from './api/client';
import type { HelloResult } from './api/types';

export default function App() {
  const [engine, setEngine] = useState<HelloResult>();
  const [error, setError] = useState<string>();
  useEffect(() => {
    let ignore = false;
    hello().then(result => { if (!ignore) setEngine(result); })
      .catch(reason => { if (!ignore) setError(String(reason)); });
    return () => { ignore = true; };
  }, []);

  return <main>
    <header><span className="mark" aria-hidden="true">↔</span><span>agents-bridge</span><span className="badge">Escritorio</span></header>
    <section aria-labelledby="title">
      <p className="eyebrow">CONEXIÓN LOCAL</p>
      <h1 id="title">Un motor.<br />Todos tus agentes.</h1>
      <p className="description">El punto de encuentro para tus agentes de código.</p>
      <div className="status" role="status" aria-live="polite">
        <span className={error ? 'dot error' : engine ? 'dot ready' : 'dot'} />
        <div><strong>{error ? 'No se pudo conectar' : engine ? 'Motor conectado' : 'Conectando con el motor…'}</strong>
          <p>{error || (engine ? `agents-bridge ${engine.engine_version} · API v${engine.contract_version}` : 'Iniciando agents-bridge')}</p>
        </div>
      </div>
    </section>
    <footer>agents-bridge · Vista inicial del escritorio</footer>
  </main>;
}
