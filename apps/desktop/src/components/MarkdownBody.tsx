import { useMemo, useState } from 'react';
import Markdown from 'react-markdown';
import rehypeHighlight from 'rehype-highlight';
import type { Root, RootContent } from 'hast';

// Literal text search after highlighting: no regex or HTML supplied by messages.
function searchMarks(query: string) {
  return () => (tree: Root) => {
    function walk(parent: Root | Exclude<RootContent, {type:'text'}>) {
      if (!('children' in parent)) return;
      parent.children = parent.children.flatMap(node => {
        if (node.type !== 'text') { walk(node); return [node]; }
        const text = node.value, lower = text.toLowerCase(), needle = query.toLowerCase();
        const parts: RootContent[] = []; let from = 0; let at = lower.indexOf(needle);
        while (at !== -1) {
          if (at > from) parts.push({type:'text', value:text.slice(from,at)});
          parts.push({type:'element', tagName:'mark', properties:{}, children:[{type:'text',value:text.slice(at,at+query.length)}]});
          from = at + query.length; at = lower.indexOf(needle, from);
        }
        if (from < text.length) parts.push({type:'text',value:text.slice(from)});
        return parts;
      }) as typeof parent.children;
    }
    if (query) walk(tree);
  };
}
export default function MarkdownBody({ body, query = '' }: {body:string; query?:string}) {
  const [expanded, setExpanded] = useState(false);
  const lines = body.split('\n'); const long = lines.length > 30;
  const searching = !!query && body.toLowerCase().includes(query.toLowerCase());
  const visible = expanded || searching || !long ? body : lines.slice(0,30).join('\n');
  const marker = useMemo(() => searchMarks(query), [query]);
  return <div className="message-body markdown"><Markdown skipHtml disallowedElements={['img']} rehypePlugins={[rehypeHighlight, marker]} components={{a:({children})=><span className="markdown-link">{children}</span>}}>{visible}</Markdown>
    {long && !searching && <button className="fold-button" aria-expanded={expanded} onClick={()=>setExpanded(!expanded)}>{expanded ? 'Mostrar menos' : `Mostrar ${lines.length-30} líneas más`}</button>}
  </div>;
}
