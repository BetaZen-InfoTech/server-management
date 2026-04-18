import React, { useEffect } from "react";
import { X } from "lucide-react";

interface ModalProps {
  isOpen: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
  size?: "sm" | "md" | "lg" | "xl";
  /**
   * Optional sticky footer — renders below the scrollable body and stays
   * pinned regardless of how far the body scrolls. Use this for Save /
   * Cancel action bars so operators never lose reach to them when
   * editing long files or scrolling long forms. Legacy callers that
   * kept their buttons inside `children` keep working — nothing breaks,
   * the buttons just aren't pinned.
   */
  footer?: React.ReactNode;
  /**
   * When true, the scrollable body fills all remaining vertical space
   * (up to the 90vh modal cap). Used by the file editor so a short file
   * doesn't leave a huge empty gap below the textarea. Default false
   * keeps the old "body hugs its content" layout that smaller forms
   * expect.
   */
  fillBody?: boolean;
}

export function Modal({
  isOpen,
  onClose,
  title,
  children,
  size = "md",
  footer,
  fillBody = false,
}: ModalProps) {
  useEffect(() => {
    const handleEsc = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    if (isOpen) document.addEventListener("keydown", handleEsc);
    return () => document.removeEventListener("keydown", handleEsc);
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  const sizes: Record<string, string> = {
    sm: "max-w-md",
    md: "max-w-lg",
    lg: "max-w-2xl",
    xl: "max-w-4xl",
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60" onClick={onClose} />
      {/*
        Flex column so header / body / footer live in three distinct
        rows. max-h-[90vh] caps the whole modal; min-h-0 on the body
        row is the standard trick to let it actually shrink instead of
        pushing the footer off-screen.
      */}
      <div
        className={`relative bg-panel-surface border border-panel-border rounded-xl shadow-2xl w-full ${sizes[size]} mx-4 max-h-[90vh] flex flex-col`}
      >
        <div className="flex items-center justify-between px-6 py-4 border-b border-panel-border shrink-0">
          <h2 className="text-lg font-semibold text-panel-text">{title}</h2>
          <button
            onClick={onClose}
            className="text-panel-muted hover:text-panel-text transition-colors"
          >
            <X size={20} />
          </button>
        </div>
        <div
          className={`px-6 py-4 overflow-y-auto min-h-0 ${
            fillBody ? "flex-1" : ""
          }`}
        >
          {children}
        </div>
        {footer && (
          <div className="shrink-0 border-t border-panel-border px-6 py-3 bg-panel-surface/95 backdrop-blur-sm">
            {footer}
          </div>
        )}
      </div>
    </div>
  );
}
