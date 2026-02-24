import React from "react";
import { Copy, Check } from "lucide-react";

interface CodeBlockProps {
  code: string;
  language?: string;
}

export function CodeBlock({ code, language = "text" }: CodeBlockProps) {
  const [copied, setCopied] = React.useState(false);

  const handleCopy = async () => {
    await navigator.clipboard.writeText(code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="relative rounded-lg bg-panel-bg border border-panel-border overflow-hidden">
      <div className="flex items-center justify-between px-4 py-2 bg-panel-surface/50 border-b border-panel-border">
        <span className="text-xs text-panel-muted">{language}</span>
        <button onClick={handleCopy} className="text-panel-muted hover:text-panel-text transition-colors">
          {copied ? <Check size={14} /> : <Copy size={14} />}
        </button>
      </div>
      <pre className="p-4 overflow-x-auto text-sm text-panel-text font-mono">
        <code>{code}</code>
      </pre>
    </div>
  );
}
