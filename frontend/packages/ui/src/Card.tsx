import React from "react";

interface CardProps {
  title?: string;
  description?: string;
  children: React.ReactNode;
  className?: string;
  actions?: React.ReactNode;
}

export function Card({ title, description, children, className = "", actions }: CardProps) {
  return (
    <div className={`bg-panel-surface border border-panel-border rounded-xl ${className}`}>
      {(title || actions) && (
        <div className="flex items-center justify-between px-6 py-4 border-b border-panel-border">
          <div>
            {title && <h3 className="text-base font-semibold text-panel-text">{title}</h3>}
            {description && <p className="text-sm text-panel-muted mt-1">{description}</p>}
          </div>
          {actions && <div className="flex items-center gap-2">{actions}</div>}
        </div>
      )}
      <div className="px-6 py-4">{children}</div>
    </div>
  );
}
