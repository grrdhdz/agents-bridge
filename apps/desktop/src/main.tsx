import { createRoot } from 'react-dom/client';
import { realClient } from './api/client';
import App from './App';
import './style.css';

async function start() {
  let client = realClient; let demo = false;
  if (import.meta.env.DEV && new URLSearchParams(location.search).get('demo') === '1') {
    const { createDemoClient } = await import('./demo/client');
    client = createDemoClient(); demo = true;
  }
  createRoot(document.getElementById('root')!).render(<App client={client} demo={demo} />);
}
void start();
