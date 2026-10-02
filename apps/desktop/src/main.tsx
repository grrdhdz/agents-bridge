import { createRoot } from 'react-dom/client';
import { realClient } from './api/client';
import App from './App';
import type { BoardPreviews } from './components/BoardPreview';
import './style.css';

async function start() {
  let client = realClient; let demo = false; let initialPreviews:BoardPreviews={};
  if (import.meta.env.DEV && new URLSearchParams(location.search).get('demo') === '1') {
    const { createDemoClient, demoPreviews } = await import('./demo/client');
    client = createDemoClient(); demo = true; initialPreviews=demoPreviews();
  }
  createRoot(document.getElementById('root')!).render(<App client={client} demo={demo} initialPreviews={initialPreviews} />);
}
void start();
