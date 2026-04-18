import { useState, useEffect, useMemo, useRef, useCallback } from "react";
import { useSearchParams } from "react-router-dom";
import { Card, Button, Modal, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  FolderOpen, File as FileIcon, Upload, FolderPlus, RefreshCw, Trash2, Download,
  ChevronRight, Home, ArrowUp, Edit, FilePlus, Archive, FileArchive, Copy,
  Scissors, Lock, Eye, EyeOff, Search, X, Check, MoreHorizontal,
  ArrowLeft, ArrowRight, KeyRound, FileText
} from "lucide-react";

interface FileItem {
  name: string;
  type: "file" | "directory" | "symlink";
  size: string;
  permissions: string;
  modified: string;
  path: string;
}

const ARCHIVE_EXTS = [".zip", ".tar", ".tar.gz", ".tgz", ".gz"];
const isArchive = (name: string) =>
  ARCHIVE_EXTS.some((ext) => name.toLowerCase().endsWith(ext));

const EDITABLE_EXTS = new Set([
  "txt", "md", "json", "js", "jsx", "ts", "tsx", "html", "htm", "css", "scss",
  "xml", "yml", "yaml", "toml", "ini", "conf", "env", "sh", "bash", "py",
  "rb", "go", "rs", "java", "c", "h", "cpp", "hpp", "php", "sql", "log",
  "gitignore", "dockerfile", "lock", "svg", "csv", "tsv",
]);
const isEditable = (name: string) => {
  const lower = name.toLowerCase();
  const ext = lower.includes(".") ? lower.split(".").pop()! : lower;
  return EDITABLE_EXTS.has(ext);
};

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const btnGhost =
  "flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm whitespace-nowrap";
const btnPrimary =
  "flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors whitespace-nowrap";

interface UploadProgress {
  name: string;
  pct: number;
  done: boolean;
  error?: string;
}

export default function FilesPage() {
  const [searchParams] = useSearchParams();
  const initialPath = searchParams.get("path") || "/var/www";

  const [files, setFiles] = useState<FileItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [currentPath, setCurrentPath] = useState(initialPath);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [lastClickedIdx, setLastClickedIdx] = useState<number | null>(null);
  const [filter, setFilter] = useState("");
  const [showHidden, setShowHidden] = useState(false);
  const [searchResults, setSearchResults] = useState<FileItem[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [isDragging, setIsDragging] = useState(false);
  const dragCounter = useRef(0);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // modals
  const [showNewFolder, setShowNewFolder] = useState(false);
  const [showNewFile, setShowNewFile] = useState(false);
  const [showUpload, setShowUpload] = useState(false);
  const [showEditor, setShowEditor] = useState(false);
  const [editorReadonly, setEditorReadonly] = useState(false);
  const [showCompress, setShowCompress] = useState(false);
  const [showCopy, setShowCopy] = useState(false);
  const [showMove, setShowMove] = useState(false);
  const [showRename, setShowRename] = useState(false);
  const [showChmod, setShowChmod] = useState(false);
  const [showProtect, setShowProtect] = useState(false);
  const [protectTarget, setProtectTarget] = useState<FileItem | null>(null);
  const [protectForm, setProtectForm] = useState({ username: "", password: "", label: "" });

  // Back/forward history — a stack of visited paths plus the cursor into it.
  // Unlike `navigateUp`, this lets you retrace the exact sequence of clicks
  // (e.g. /a → /a/b → back to /a → forward to /a/b), matching browser tabs.
  const [history, setHistory] = useState<string[]>([initialPath]);
  const [historyIdx, setHistoryIdx] = useState(0);
  const canGoBack = historyIdx > 0;
  const canGoForward = historyIdx < history.length - 1;

  // modal form state
  const [folderName, setFolderName] = useState("");
  const [newFileName, setNewFileName] = useState("");
  const [uploadFiles, setUploadFiles] = useState<File[]>([]);
  const [uploadProgress, setUploadProgress] = useState<UploadProgress[]>([]);
  const [uploading, setUploading] = useState(false);
  const [editingFile, setEditingFile] = useState<FileItem | null>(null);
  const [fileContent, setFileContent] = useState("");
  const [loadingFile, setLoadingFile] = useState(false);
  const [saving, setSaving] = useState(false);
  const [archiveName, setArchiveName] = useState("");
  const [archiveFormat, setArchiveFormat] = useState<"zip" | "tar.gz">("zip");
  const [destPath, setDestPath] = useState("");
  const [renameTarget, setRenameTarget] = useState<FileItem | null>(null);
  const [renameNew, setRenameNew] = useState("");
  const [chmodTarget, setChmodTarget] = useState<FileItem | null>(null);
  const [chmodValue, setChmodValue] = useState("644");
  const [chmodRecursive, setChmodRecursive] = useState(false);

  // context menu
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; file: FileItem } | null>(null);

  useEffect(() => {
    fetchFiles();
    setSelected(new Set());
    setSearchResults(null);
    setFilter("");
  }, [currentPath]);

  useEffect(() => {
    const close = () => setCtxMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("scroll", close, true);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("scroll", close, true);
    };
  }, []);

  const fetchFiles = async () => {
    setLoading(true);
    try {
      const res = await api.get("/files/list", { params: { path: currentPath } });
      setFiles(res.data.data || []);
    } catch {
      setFiles([]);
    } finally {
      setLoading(false);
    }
  };

  const runSearch = async (q: string) => {
    if (!q.trim()) {
      setSearchResults(null);
      return;
    }
    setSearching(true);
    try {
      const res = await api.get("/files/search", { params: { path: currentPath, q } });
      setSearchResults(res.data.data || []);
    } catch {
      toast.error("Search failed");
    } finally {
      setSearching(false);
    }
  };

  const visibleFiles = useMemo(() => {
    const source = searchResults ?? files;
    return source.filter((f) => {
      if (!showHidden && f.name.startsWith(".")) return false;
      if (filter && !f.name.toLowerCase().includes(filter.toLowerCase())) return false;
      return true;
    });
  }, [files, searchResults, filter, showHidden]);

  const allSelected = visibleFiles.length > 0 && visibleFiles.every((f) => selected.has(f.path));
  const selectedFiles = files.filter((f) => selected.has(f.path));
  const selectedCount = selected.size;

  const toggleSelect = (path: string, idx: number, e?: React.MouseEvent) => {
    const next = new Set(selected);
    if (e?.shiftKey && lastClickedIdx !== null) {
      const [a, b] = [lastClickedIdx, idx].sort((x, y) => x - y);
      for (let i = a; i <= b; i++) next.add(visibleFiles[i].path);
    } else {
      if (next.has(path)) next.delete(path);
      else next.add(path);
    }
    setSelected(next);
    setLastClickedIdx(idx);
  };

  const toggleSelectAll = () => {
    if (allSelected) setSelected(new Set());
    else setSelected(new Set(visibleFiles.map((f) => f.path)));
  };

  const clearSelection = () => setSelected(new Set());

  const navigateTo = (path: string) => {
    if (path === currentPath) return;
    // Drop any "forward" history entries past the current cursor so the
    // stack always reflects the actual linear path the user walked.
    setHistory((prev) => [...prev.slice(0, historyIdx + 1), path]);
    setHistoryIdx((prev) => prev + 1);
    setCurrentPath(path);
  };
  const navigateUp = () => {
    const parts = currentPath.split("/").filter(Boolean);
    if (parts.length >= 1) {
      parts.pop();
      navigateTo("/" + parts.join("/"));
    }
  };
  const goBack = () => {
    if (!canGoBack) return;
    const newIdx = historyIdx - 1;
    setHistoryIdx(newIdx);
    setCurrentPath(history[newIdx]);
  };
  const goForward = () => {
    if (!canGoForward) return;
    const newIdx = historyIdx + 1;
    setHistoryIdx(newIdx);
    setCurrentPath(history[newIdx]);
  };

  // ---- actions ----

  const handleCreateFolder = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!folderName.trim()) return toast.error("Please enter a folder name");
    try {
      await api.post("/files/create", {
        path: `${currentPath}/${folderName}`,
        type: "directory",
      });
      toast.success(`Folder "${folderName}" created`);
      setShowNewFolder(false);
      setFolderName("");
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create folder");
    }
  };

  const handleCreateFile = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newFileName.trim()) return toast.error("Please enter a file name");
    try {
      await api.post("/files/create", {
        path: `${currentPath}/${newFileName}`,
        content: "",
      });
      toast.success(`File "${newFileName}" created`);
      setShowNewFile(false);
      setNewFileName("");
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create file");
    }
  };

  const uploadBatch = useCallback(
    async (batch: File[]) => {
      if (batch.length === 0) return;

      // Client-side size guard — nginx + the Fiber BodyLimit both cap
      // individual requests at 500 MB, so rejecting oversized files up
      // front saves the user a long upload that ends in 413.
      const MAX_UPLOAD_BYTES = 500 * 1024 * 1024;
      const tooBig = batch.filter((f) => f.size > MAX_UPLOAD_BYTES);
      if (tooBig.length > 0) {
        toast.error(
          `${tooBig.length === 1 ? tooBig[0].name : `${tooBig.length} files`} exceed the 500 MB per-file limit`
        );
        const kept = batch.filter((f) => f.size <= MAX_UPLOAD_BYTES);
        if (kept.length === 0) return;
        batch = kept;
      }

      setUploading(true);
      setUploadProgress(batch.map((f) => ({ name: f.name, pct: 0, done: false })));

      // Upload sequentially so each file's progress bar is clear.
      for (let i = 0; i < batch.length; i++) {
        const f = batch[i];
        try {
          const fd = new FormData();
          fd.append("files", f);
          fd.append("path", currentPath);
          await api.post("/files/upload", fd, {
            headers: { "Content-Type": "multipart/form-data" },
            onUploadProgress: (ev) => {
              if (!ev.total) return;
              const pct = Math.round((ev.loaded / ev.total) * 100);
              setUploadProgress((prev) =>
                prev.map((p, idx) => (idx === i ? { ...p, pct } : p))
              );
            },
          });
          setUploadProgress((prev) =>
            prev.map((p, idx) => (idx === i ? { ...p, pct: 100, done: true } : p))
          );
        } catch (err: any) {
          setUploadProgress((prev) =>
            prev.map((p, idx) =>
              idx === i
                ? { ...p, error: err?.response?.data?.error?.message || "Failed", done: true }
                : p
            )
          );
        }
      }

      setUploading(false);
      toast.success(`Uploaded ${batch.length} file(s)`);
      fetchFiles();
    },
    [currentPath]
  );

  const handleUploadSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (uploadFiles.length === 0) return toast.error("Please select files");
    await uploadBatch(uploadFiles);
    setUploadFiles([]);
    setTimeout(() => {
      setShowUpload(false);
      setUploadProgress([]);
    }, 800);
  };

  const handleOpenEditor = async (item: FileItem, readonly = false) => {
    setEditingFile(item);
    setEditorReadonly(readonly);
    setShowEditor(true);
    setLoadingFile(true);
    try {
      const res = await api.get("/files/read", { params: { path: item.path } });
      setFileContent(res.data.data?.content ?? "");
    } catch {
      toast.error("Failed to read file");
      setFileContent("");
    } finally {
      setLoadingFile(false);
    }
  };

  const handleProtectSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!protectTarget) return;
    if (!protectForm.username.trim()) return toast.error("Username is required");
    try {
      await api.post("/files/protect", {
        path: protectTarget.path,
        username: protectForm.username,
        password: protectForm.password,
        label: protectForm.label,
      });
      toast.success(`Protected ${protectTarget.name}`);
      setShowProtect(false);
      setProtectTarget(null);
      setProtectForm({ username: "", password: "", label: "" });
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Protect failed");
    }
  };

  const handleUnprotect = async (item: FileItem) => {
    if (!await confirmAction({ title: "Remove?", description: `Remove password protection from "${item.name}"?`, danger: true, confirmLabel: "Remove" })) return;
    try {
      await api.post("/files/unprotect", { path: item.path });
      toast.success("Protection removed");
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Unprotect failed");
    }
  };

  const handleSaveFile = async () => {
    if (!editingFile) return;
    setSaving(true);
    try {
      await api.put("/files/edit", { path: editingFile.path, content: fileContent });
      toast.success(`${editingFile.name} saved`);
      setShowEditor(false);
      setEditingFile(null);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to save file");
    } finally {
      setSaving(false);
    }
  };

  const handleDownload = async (item: FileItem) => {
    try {
      const res = await api.get("/files/download", {
        params: { path: item.path },
        responseType: "blob",
      });
      const url = URL.createObjectURL(res.data);
      const a = document.createElement("a");
      a.href = url;
      a.download = item.name;
      a.click();
      URL.revokeObjectURL(url);
    } catch {
      toast.error("Failed to download file");
    }
  };

  const handleDeleteOne = async (item: FileItem) => {
    if (!await confirmAction({ title: "Delete?", description: `Delete "${item.name}"? This cannot be undone.`, danger: true, confirmLabel: "Delete" })) return;
    try {
      await api.delete("/files/delete", { data: { path: item.path } });
      toast.success(`${item.name} deleted`);
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to delete");
    }
  };

  const handleBulkDelete = async () => {
    if (selectedCount === 0) return;
    if (!await confirmAction({ title: "Delete?", description: `Delete ${selectedCount} item(s)? This cannot be undone.`, danger: true, confirmLabel: "Delete" })) return;
    let ok = 0;
    for (const f of selectedFiles) {
      try {
        await api.delete("/files/delete", { data: { path: f.path } });
        ok++;
      } catch {
        /* keep going */
      }
    }
    toast.success(`Deleted ${ok}/${selectedCount} item(s)`);
    clearSelection();
    fetchFiles();
  };

  const handleExtract = async (item: FileItem) => {
    if (!await confirmAction({ title: "Extract?", description: `Extract "${item.name}" into ${currentPath}?`, confirmLabel: "Extract" })) return;
    try {
      await api.post("/files/extract", { archive: item.path, destination: currentPath });
      toast.success(`${item.name} extracted`);
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to extract");
    }
  };

  const handleCompressSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!archiveName.trim()) return toast.error("Enter an archive name");
    if (selectedCount === 0) return toast.error("Select files to compress");
    const ext = archiveFormat === "zip" ? ".zip" : ".tar.gz";
    const name = archiveName.endsWith(ext) ? archiveName : archiveName + ext;
    try {
      await api.post("/files/compress", {
        paths: selectedFiles.map((f) => f.path),
        output: `${currentPath}/${name}`,
        format: archiveFormat,
      });
      toast.success(`Created ${name}`);
      setShowCompress(false);
      setArchiveName("");
      clearSelection();
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Compression failed");
    }
  };

  const handleCopySubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!destPath.trim()) return toast.error("Enter destination path");
    try {
      await api.post("/files/copy", {
        sources: selectedFiles.map((f) => f.path),
        destination: destPath,
      });
      toast.success(`Copied ${selectedCount} item(s)`);
      setShowCopy(false);
      setDestPath("");
      clearSelection();
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Copy failed");
    }
  };

  const handleMoveSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!destPath.trim()) return toast.error("Enter destination path");
    try {
      await api.post("/files/move", {
        sources: selectedFiles.map((f) => f.path),
        destination: destPath,
      });
      toast.success(`Moved ${selectedCount} item(s)`);
      setShowMove(false);
      setDestPath("");
      clearSelection();
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Move failed");
    }
  };

  const handleRenameSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!renameTarget || !renameNew.trim()) return;
    const parent = renameTarget.path.substring(0, renameTarget.path.lastIndexOf("/"));
    try {
      await api.post("/files/rename", {
        source: renameTarget.path,
        destination: `${parent}/${renameNew}`,
      });
      toast.success("Renamed");
      setShowRename(false);
      setRenameTarget(null);
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Rename failed");
    }
  };

  const handleChmodSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!chmodTarget) return;
    if (!/^[0-7]{3,4}$/.test(chmodValue)) return toast.error("Enter octal (e.g. 755)");
    try {
      await api.post("/files/chmod", {
        path: chmodTarget.path,
        permissions: chmodValue,
        recursive: chmodRecursive,
      });
      toast.success("Permissions updated");
      setShowChmod(false);
      setChmodTarget(null);
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Chmod failed");
    }
  };

  // ---- drag and drop ----

  const onDragEnter = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounter.current++;
    if (e.dataTransfer.items && e.dataTransfer.items.length > 0) setIsDragging(true);
  };
  const onDragLeave = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounter.current--;
    if (dragCounter.current === 0) setIsDragging(false);
  };
  const onDragOver = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  };
  const onDrop = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(false);
    dragCounter.current = 0;
    const dropped = Array.from(e.dataTransfer.files || []);
    if (dropped.length > 0) uploadBatch(dropped);
  };

  const breadcrumbs = currentPath.split("/").filter(Boolean);
  const editorLang = editingFile?.name.split(".").pop()?.toLowerCase() || "";
  const lineCount = Math.max(fileContent.split("\n").length, 1);

  return (
    <div
      className="space-y-6 relative"
      onDragEnter={onDragEnter}
      onDragLeave={onDragLeave}
      onDragOver={onDragOver}
      onDrop={onDrop}
    >
      {/* Drag overlay */}
      {isDragging && (
        <div className="fixed inset-0 z-50 bg-blue-500/10 backdrop-blur-sm border-4 border-dashed border-blue-500 flex items-center justify-center pointer-events-none">
          <div className="text-center">
            <Upload size={64} className="text-blue-400 mx-auto mb-4" />
            <p className="text-2xl font-bold text-panel-text">Drop files to upload</p>
            <p className="text-panel-muted mt-2">Uploads into {currentPath}</p>
          </div>
        </div>
      )}

      {/* Header */}
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-xl font-bold text-panel-text">File Manager</h1>
          <p className="text-panel-muted text-sm mt-1">Browse and manage server files</p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <Button onClick={fetchFiles} className={btnGhost}>
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button onClick={() => setShowHidden((s) => !s)} className={btnGhost}>
            {showHidden ? <EyeOff size={14} /> : <Eye size={14} />}
            {showHidden ? "Hide" : "Show"} hidden
          </Button>
          <Button onClick={() => setShowNewFile(true)} className={btnGhost}>
            <FilePlus size={14} />
            New File
          </Button>
          <Button onClick={() => setShowNewFolder(true)} className={btnGhost}>
            <FolderPlus size={14} />
            New Folder
          </Button>
          <Button onClick={() => setShowUpload(true)} className={btnPrimary}>
            <Upload size={14} />
            Upload
          </Button>
        </div>
      </div>

      {/* Breadcrumb + search */}
      <Card>
        <div className="p-3 flex items-center gap-3 flex-wrap">
          <div className="flex items-center gap-1 overflow-x-auto flex-1 min-w-0">
            <button
              onClick={goBack}
              disabled={!canGoBack}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-panel-text transition-colors shrink-0 disabled:opacity-30 disabled:cursor-not-allowed"
              title="Back"
            >
              <ArrowLeft size={14} />
            </button>
            <button
              onClick={goForward}
              disabled={!canGoForward}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-panel-text transition-colors shrink-0 disabled:opacity-30 disabled:cursor-not-allowed"
              title="Forward"
            >
              <ArrowRight size={14} />
            </button>
            <button
              onClick={() => navigateTo("/")}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-panel-text transition-colors shrink-0"
              title="Root"
            >
              <Home size={14} />
            </button>
            <button
              onClick={navigateUp}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-panel-text transition-colors shrink-0"
              title="Go up"
            >
              <ArrowUp size={14} />
            </button>
            <ChevronRight size={12} className="text-panel-muted/40 shrink-0" />
            {breadcrumbs.map((part, index) => (
              <div key={index} className="flex items-center gap-1 shrink-0">
                <button
                  onClick={() => navigateTo("/" + breadcrumbs.slice(0, index + 1).join("/"))}
                  className="px-2 py-1 rounded text-sm text-panel-muted hover:text-panel-text hover:bg-panel-bg transition-colors"
                >
                  {part}
                </button>
                {index < breadcrumbs.length - 1 && (
                  <ChevronRight size={12} className="text-panel-muted/40" />
                )}
              </div>
            ))}
          </div>
          <div className="flex items-center gap-2 shrink-0">
            <div className="relative">
              <Search
                size={14}
                className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
              />
              <input
                type="text"
                placeholder="Filter / Enter to search recursively"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") runSearch(filter);
                  if (e.key === "Escape") {
                    setFilter("");
                    setSearchResults(null);
                  }
                }}
                className={`${inputClass} pl-9 pr-9 w-72`}
              />
              {(filter || searchResults) && (
                <button
                  onClick={() => {
                    setFilter("");
                    setSearchResults(null);
                  }}
                  className="absolute right-2 top-1/2 -translate-y-1/2 p-1 rounded hover:bg-panel-bg text-panel-muted"
                >
                  <X size={12} />
                </button>
              )}
            </div>
          </div>
        </div>
        {searchResults && (
          <div className="px-3 pb-2 text-xs text-panel-muted">
            {searching ? "Searching…" : `Showing ${searchResults.length} search result(s) under ${currentPath}`}
          </div>
        )}
      </Card>

      {/* Quick selection bar — mirrors cPanel's "Select All / Unselect All"
          links so users don't have to hunt for the header checkbox. */}
      <div className="flex items-center gap-2 text-xs text-panel-muted px-1">
        <button
          onClick={() => setSelected(new Set(visibleFiles.map((f) => f.path)))}
          className="hover:text-panel-text transition-colors"
        >
          Select All
        </button>
        <span>·</span>
        <button
          onClick={clearSelection}
          className="hover:text-panel-text transition-colors"
        >
          Unselect All
        </button>
        <span>·</span>
        <span>{visibleFiles.length} item(s)</span>
        {selectedCount > 0 && (
          <>
            <span>·</span>
            <span className="text-blue-400">{selectedCount} selected</span>
          </>
        )}
      </div>

      {/* Bulk action bar */}
      {selectedCount > 0 && (
        <div className="sticky top-0 z-10 bg-blue-600/10 border border-blue-500/30 rounded-lg p-3 flex items-center gap-2 flex-wrap">
          <div className="text-sm text-panel-text font-medium mr-2">
            {selectedCount} selected
          </div>
          <button onClick={() => setShowCompress(true)} className={btnGhost}>
            <Archive size={14} />
            Compress
          </button>
          <button
            onClick={() => {
              setDestPath(currentPath);
              setShowCopy(true);
            }}
            className={btnGhost}
          >
            <Copy size={14} />
            Copy to…
          </button>
          <button
            onClick={() => {
              setDestPath(currentPath);
              setShowMove(true);
            }}
            className={btnGhost}
          >
            <Scissors size={14} />
            Move to…
          </button>
          <button onClick={handleBulkDelete} className={`${btnGhost} hover:text-red-400`}>
            <Trash2 size={14} />
            Delete
          </button>
          <button onClick={clearSelection} className="ml-auto text-sm text-panel-muted hover:text-panel-text px-2">
            Clear
          </button>
        </div>
      )}

      {/* File list */}
      <Card>
        {loading ? (
          <div className="p-8 space-y-3">
            {[1, 2, 3, 4, 5, 6].map((i) => (
              <div key={i} className="h-10 bg-panel-border/20 rounded animate-pulse" />
            ))}
          </div>
        ) : visibleFiles.length > 0 ? (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-panel-border text-left text-panel-muted text-xs uppercase tracking-wider">
                  <th className="p-3 w-10">
                    <input
                      type="checkbox"
                      checked={allSelected}
                      onChange={toggleSelectAll}
                      className="accent-blue-500 cursor-pointer"
                    />
                  </th>
                  <th className="p-3">Name</th>
                  <th className="p-3 w-24">Size</th>
                  <th className="p-3 w-32">Permissions</th>
                  <th className="p-3 w-40">Modified</th>
                  <th className="p-3 w-40 text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {visibleFiles.map((f, idx) => {
                  const isSel = selected.has(f.path);
                  return (
                    <tr
                      key={f.path}
                      className={`border-b border-panel-border/50 hover:bg-panel-bg/50 transition-colors ${
                        isSel ? "bg-blue-500/10" : ""
                      }`}
                      onContextMenu={(e) => {
                        e.preventDefault();
                        setCtxMenu({ x: e.clientX, y: e.clientY, file: f });
                      }}
                      onDoubleClick={() => {
                        if (f.type === "directory") navigateTo(f.path);
                        else if (isEditable(f.name)) handleOpenEditor(f);
                      }}
                    >
                      <td className="p-3">
                        <input
                          type="checkbox"
                          checked={isSel}
                          onChange={() => {}}
                          onClick={(e) => toggleSelect(f.path, idx, e)}
                          className="accent-blue-500 cursor-pointer"
                        />
                      </td>
                      <td className="p-3">
                        <button
                          onClick={() => f.type === "directory" && navigateTo(f.path)}
                          className={`flex items-center gap-2 ${
                            f.type === "directory" ? "cursor-pointer hover:text-blue-400" : ""
                          }`}
                        >
                          {f.type === "directory" ? (
                            <FolderOpen size={16} className="text-yellow-400 shrink-0" />
                          ) : isArchive(f.name) ? (
                            <FileArchive size={16} className="text-purple-400 shrink-0" />
                          ) : (
                            <FileIcon size={16} className="text-panel-muted shrink-0" />
                          )}
                          <span
                            className={`font-medium ${
                              f.type === "directory" ? "text-panel-text" : "text-panel-muted"
                            }`}
                          >
                            {f.name}
                          </span>
                        </button>
                      </td>
                      <td className="p-3 text-panel-muted">
                        {f.type === "directory" ? "--" : f.size}
                      </td>
                      <td className="p-3">
                        <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">
                          {f.permissions}
                        </code>
                      </td>
                      <td className="p-3 text-panel-muted">{f.modified}</td>
                      <td className="p-3">
                        <div className="flex items-center gap-1 justify-end">
                          {f.type === "file" && isEditable(f.name) && (
                            <button
                              onClick={() => handleOpenEditor(f, true)}
                              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-cyan-400 transition-colors"
                              title="View"
                            >
                              <FileText size={14} />
                            </button>
                          )}
                          {f.type === "file" && isEditable(f.name) && (
                            <button
                              onClick={() => handleOpenEditor(f)}
                              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
                              title="Edit"
                            >
                              <Edit size={14} />
                            </button>
                          )}
                          {f.type === "file" && isArchive(f.name) && (
                            <button
                              onClick={() => handleExtract(f)}
                              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-purple-400 transition-colors"
                              title="Extract"
                            >
                              <FileArchive size={14} />
                            </button>
                          )}
                          {f.type === "file" && (
                            <button
                              onClick={() => handleDownload(f)}
                              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
                              title="Download"
                            >
                              <Download size={14} />
                            </button>
                          )}
                          <button
                            onClick={(e) => {
                              e.stopPropagation();
                              setCtxMenu({ x: e.clientX, y: e.clientY, file: f });
                            }}
                            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-panel-text transition-colors"
                            title="More"
                          >
                            <MoreHorizontal size={14} />
                          </button>
                          <button
                            onClick={() => handleDeleteOne(f)}
                            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
                            title="Delete"
                          >
                            <Trash2 size={14} />
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="text-center py-16 px-4">
            <FolderOpen size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">
              {searchResults ? "No matches" : "Empty directory"}
            </h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {searchResults
                ? "No files match your search."
                : "Drop files anywhere on this page to upload, or use the buttons above."}
            </p>
          </div>
        )}
      </Card>

      {/* Context menu */}
      {ctxMenu && (() => {
        // Clamp against the viewport so menus near the right/bottom edges
        // don't overflow. 220×380 is a generous bounding box for the menu.
        const MENU_W = 220, MENU_H = 380;
        const vw = typeof window !== "undefined" ? window.innerWidth : 1200;
        const vh = typeof window !== "undefined" ? window.innerHeight : 800;
        const left = Math.min(ctxMenu.x, vw - MENU_W - 8);
        const top = Math.min(ctxMenu.y, vh - MENU_H - 8);
        const f = ctxMenu.file;
        const close = () => setCtxMenu(null);
        const itemClass = "w-full text-left px-3 py-1.5 text-sm text-panel-text hover:bg-panel-bg flex items-center gap-2";
        return (
          <div
            className="fixed z-50 bg-panel-surface border border-panel-border rounded-lg shadow-xl py-1 min-w-[200px]"
            style={{ left, top }}
            onClick={(e) => e.stopPropagation()}
          >
            {/* Order mirrors cPanel's right-click menu so users with cPanel
                muscle memory find the same items in the same place. */}
            {f.type === "directory" && (
              <button onClick={() => { navigateTo(f.path); close(); }} className={itemClass}>
                <FolderOpen size={12} /> Open
              </button>
            )}
            {f.type === "file" && (
              <button onClick={() => { handleDownload(f); close(); }} className={itemClass}>
                <Download size={12} /> Download
              </button>
            )}
            {f.type === "file" && isEditable(f.name) && (
              <button onClick={() => { handleOpenEditor(f, true); close(); }} className={itemClass}>
                <Eye size={12} /> View
              </button>
            )}
            {f.type === "file" && isEditable(f.name) && (
              <button onClick={() => { handleOpenEditor(f); close(); }} className={itemClass}>
                <Edit size={12} /> Edit
              </button>
            )}
            <button
              onClick={() => {
                setSelected(new Set([f.path]));
                setDestPath(currentPath);
                setShowMove(true);
                close();
              }}
              className={itemClass}
            >
              <Scissors size={12} /> Move
            </button>
            <button
              onClick={() => {
                setSelected(new Set([f.path]));
                setDestPath(currentPath);
                setShowCopy(true);
                close();
              }}
              className={itemClass}
            >
              <Copy size={12} /> Copy
            </button>
            <button
              onClick={() => {
                setRenameTarget(f);
                setRenameNew(f.name);
                setShowRename(true);
                close();
              }}
              className={itemClass}
            >
              <Edit size={12} /> Rename
            </button>
            <button
              onClick={() => {
                setChmodTarget(f);
                setChmodValue(f.type === "directory" ? "755" : "644");
                setShowChmod(true);
                close();
              }}
              className={itemClass}
            >
              <Lock size={12} /> Change Permissions
            </button>
            <div className="border-t border-panel-border my-1" />
            {f.type === "file" && isArchive(f.name) && (
              <button onClick={() => { handleExtract(f); close(); }} className={itemClass}>
                <FileArchive size={12} /> Extract
              </button>
            )}
            <button
              onClick={() => {
                // Seed selection with this file so the Compress modal uses it
                setSelected(new Set([f.path]));
                setArchiveName(`${f.name}.zip`);
                setShowCompress(true);
                close();
              }}
              className={itemClass}
            >
              <Archive size={12} /> Compress
            </button>
            {f.type === "directory" && (
              <>
                <div className="border-t border-panel-border my-1" />
                <button
                  onClick={() => {
                    setProtectTarget(f);
                    setProtectForm({ username: "", password: "", label: f.name });
                    setShowProtect(true);
                    close();
                  }}
                  className={itemClass}
                >
                  <KeyRound size={12} /> Password Protect
                </button>
                <button
                  onClick={() => { handleUnprotect(f); close(); }}
                  className={itemClass}
                >
                  <KeyRound size={12} /> Remove Protection
                </button>
              </>
            )}
            <div className="border-t border-panel-border my-1" />
            <button
              onClick={() => { handleDeleteOne(f); close(); }}
              className="w-full text-left px-3 py-1.5 text-sm text-red-400 hover:bg-panel-bg flex items-center gap-2"
            >
              <Trash2 size={12} /> Delete
            </button>
          </div>
        );
      })()}

      {/* New Folder Modal */}
      <Modal
        isOpen={showNewFolder}
        onClose={() => setShowNewFolder(false)}
        title="New Folder"
        size="sm"
      >
        <form onSubmit={handleCreateFolder} className="space-y-4">
          <div>
            <label className={labelClass}>Folder Name *</label>
            <input
              type="text"
              required
              placeholder="my-folder"
              value={folderName}
              onChange={(e) => setFolderName(e.target.value)}
              className={inputClass}
              autoFocus
            />
            <p className="text-xs text-panel-muted mt-1">Will be created in: {currentPath}/</p>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setShowNewFolder(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg"
            >
              Cancel
            </button>
            <button type="submit" className={btnPrimary}>
              Create
            </button>
          </div>
        </form>
      </Modal>

      {/* New File Modal */}
      <Modal
        isOpen={showNewFile}
        onClose={() => setShowNewFile(false)}
        title="New File"
        size="sm"
      >
        <form onSubmit={handleCreateFile} className="space-y-4">
          <div>
            <label className={labelClass}>File Name *</label>
            <input
              type="text"
              required
              placeholder="index.html"
              value={newFileName}
              onChange={(e) => setNewFileName(e.target.value)}
              className={inputClass}
              autoFocus
            />
            <p className="text-xs text-panel-muted mt-1">
              Empty file will be created in: {currentPath}/
            </p>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setShowNewFile(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg"
            >
              Cancel
            </button>
            <button type="submit" className={btnPrimary}>
              Create
            </button>
          </div>
        </form>
      </Modal>

      {/* Upload Modal */}
      <Modal
        isOpen={showUpload}
        onClose={() => {
          if (uploading) return;
          setShowUpload(false);
          setUploadFiles([]);
          setUploadProgress([]);
        }}
        title="Upload Files"
        size="md"
      >
        <form onSubmit={handleUploadSubmit} className="space-y-4">
          <div
            onClick={() => fileInputRef.current?.click()}
            className="border-2 border-dashed border-panel-border rounded-lg p-6 text-center hover:border-blue-500/40 transition-colors cursor-pointer"
          >
            <Upload size={24} className="text-panel-muted mx-auto mb-2" />
            <p className="text-sm text-panel-text font-medium">Click to select files</p>
            <p className="text-xs text-panel-muted mt-1">or drop files anywhere on this page</p>
            <p className="text-[10px] text-panel-muted/70 mt-2">Max 500 MB per file</p>
            <input
              ref={fileInputRef}
              type="file"
              multiple
              onChange={(e) => setUploadFiles(Array.from(e.target.files || []))}
              className="hidden"
            />
          </div>
          {uploadFiles.length > 0 && uploadProgress.length === 0 && (
            <div className="max-h-40 overflow-y-auto space-y-1">
              {uploadFiles.map((f, i) => (
                <div
                  key={i}
                  className="text-xs flex items-center justify-between bg-panel-bg p-2 rounded"
                >
                  <span className="text-panel-text truncate">{f.name}</span>
                  <span className="text-panel-muted ml-2">{(f.size / 1024).toFixed(1)} KB</span>
                </div>
              ))}
            </div>
          )}
          {uploadProgress.length > 0 && (
            <div className="max-h-48 overflow-y-auto space-y-2">
              {uploadProgress.map((p, i) => (
                <div key={i} className="text-xs">
                  <div className="flex items-center justify-between mb-1">
                    <span className="text-panel-text truncate flex items-center gap-1">
                      {p.done && !p.error && <Check size={12} className="text-green-400" />}
                      {p.error && <X size={12} className="text-red-400" />}
                      {p.name}
                    </span>
                    <span className="text-panel-muted ml-2">
                      {p.error ? p.error : `${p.pct}%`}
                    </span>
                  </div>
                  <div className="h-1 bg-panel-bg rounded overflow-hidden">
                    <div
                      className={`h-full ${p.error ? "bg-red-500" : "bg-blue-500"} transition-all`}
                      style={{ width: `${p.pct}%` }}
                    />
                  </div>
                </div>
              ))}
            </div>
          )}
          <p className="text-xs text-panel-muted">Upload to: {currentPath}/</p>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              disabled={uploading}
              onClick={() => {
                setShowUpload(false);
                setUploadFiles([]);
                setUploadProgress([]);
              }}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg disabled:opacity-50"
            >
              Close
            </button>
            <button
              type="submit"
              disabled={uploading || uploadFiles.length === 0}
              className={`${btnPrimary} disabled:opacity-50`}
            >
              {uploading ? "Uploading…" : `Upload ${uploadFiles.length || ""} file(s)`}
            </button>
          </div>
        </form>
      </Modal>

      {/* Editor Modal */}
      <Modal
        isOpen={showEditor}
        onClose={() => {
          setShowEditor(false);
          setEditingFile(null);
          setEditorReadonly(false);
        }}
        title={`${editorReadonly ? "View" : "Edit"} — ${editingFile?.name || ""}`}
        size="lg"
      >
        <div className="space-y-4">
          {loadingFile ? (
            <div className="h-80 bg-panel-border/20 rounded animate-pulse" />
          ) : (
            <>
              <div className="flex items-center justify-between text-xs">
                <code className="text-panel-muted font-mono truncate">{editingFile?.path}</code>
                <span className="text-panel-muted uppercase tracking-wider">
                  {editorLang} · {lineCount} lines
                </span>
              </div>
              <div className="flex border border-panel-border rounded-lg overflow-hidden bg-panel-bg">
                <pre
                  className="py-2 px-3 text-right text-xs font-mono text-panel-muted/50 select-none bg-panel-bg border-r border-panel-border overflow-hidden"
                  style={{ minWidth: "3rem" }}
                >
                  {Array.from({ length: lineCount }, (_, i) => i + 1).join("\n")}
                </pre>
                <textarea
                  value={fileContent}
                  readOnly={editorReadonly}
                  onChange={(e) => setFileContent(e.target.value)}
                  onKeyDown={(e) => {
                    if (editorReadonly) return;
                    if (e.key === "Tab") {
                      e.preventDefault();
                      const t = e.currentTarget;
                      const start = t.selectionStart;
                      const end = t.selectionEnd;
                      const next =
                        fileContent.substring(0, start) + "  " + fileContent.substring(end);
                      setFileContent(next);
                      requestAnimationFrame(() => {
                        t.selectionStart = t.selectionEnd = start + 2;
                      });
                    }
                  }}
                  className="flex-1 py-2 px-3 h-96 bg-panel-bg text-panel-text font-mono text-sm focus:outline-none resize-none"
                  spellCheck={false}
                />
              </div>
            </>
          )}
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => {
                setShowEditor(false);
                setEditingFile(null);
                setEditorReadonly(false);
              }}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg"
            >
              {editorReadonly ? "Close" : "Cancel"}
            </button>
            {!editorReadonly && (
              <button
                onClick={handleSaveFile}
                disabled={saving || loadingFile}
                className={`${btnPrimary} disabled:opacity-50`}
              >
                <Edit size={14} />
                {saving ? "Saving…" : "Save"}
              </button>
            )}
          </div>
        </div>
      </Modal>

      {/* Compress Modal */}
      <Modal
        isOpen={showCompress}
        onClose={() => setShowCompress(false)}
        title={`Compress ${selectedCount} item(s)`}
        size="sm"
      >
        <form onSubmit={handleCompressSubmit} className="space-y-4">
          <div>
            <label className={labelClass}>Archive name *</label>
            <input
              type="text"
              required
              placeholder="archive"
              value={archiveName}
              onChange={(e) => setArchiveName(e.target.value)}
              className={inputClass}
              autoFocus
            />
          </div>
          <div>
            <label className={labelClass}>Format</label>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => setArchiveFormat("zip")}
                className={`flex-1 px-3 py-2 rounded-lg text-sm border transition-colors ${
                  archiveFormat === "zip"
                    ? "bg-blue-600 border-blue-600 text-white"
                    : "bg-panel-bg border-panel-border text-panel-muted"
                }`}
              >
                ZIP
              </button>
              <button
                type="button"
                onClick={() => setArchiveFormat("tar.gz")}
                className={`flex-1 px-3 py-2 rounded-lg text-sm border transition-colors ${
                  archiveFormat === "tar.gz"
                    ? "bg-blue-600 border-blue-600 text-white"
                    : "bg-panel-bg border-panel-border text-panel-muted"
                }`}
              >
                TAR.GZ
              </button>
            </div>
          </div>
          <p className="text-xs text-panel-muted">
            Archive will be created in {currentPath}/
          </p>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setShowCompress(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg"
            >
              Cancel
            </button>
            <button type="submit" className={btnPrimary}>
              Compress
            </button>
          </div>
        </form>
      </Modal>

      {/* Copy Modal */}
      <Modal isOpen={showCopy} onClose={() => setShowCopy(false)} title={`Copy ${selectedCount} item(s)`} size="sm">
        <form onSubmit={handleCopySubmit} className="space-y-4">
          <div>
            <label className={labelClass}>Destination folder *</label>
            <input
              type="text"
              required
              value={destPath}
              onChange={(e) => setDestPath(e.target.value)}
              className={inputClass}
              autoFocus
            />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setShowCopy(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg"
            >
              Cancel
            </button>
            <button type="submit" className={btnPrimary}>
              Copy
            </button>
          </div>
        </form>
      </Modal>

      {/* Move Modal */}
      <Modal isOpen={showMove} onClose={() => setShowMove(false)} title={`Move ${selectedCount} item(s)`} size="sm">
        <form onSubmit={handleMoveSubmit} className="space-y-4">
          <div>
            <label className={labelClass}>Destination folder *</label>
            <input
              type="text"
              required
              value={destPath}
              onChange={(e) => setDestPath(e.target.value)}
              className={inputClass}
              autoFocus
            />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setShowMove(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg"
            >
              Cancel
            </button>
            <button type="submit" className={btnPrimary}>
              Move
            </button>
          </div>
        </form>
      </Modal>

      {/* Rename Modal */}
      <Modal isOpen={showRename} onClose={() => setShowRename(false)} title="Rename" size="sm">
        <form onSubmit={handleRenameSubmit} className="space-y-4">
          <div>
            <label className={labelClass}>New name *</label>
            <input
              type="text"
              required
              value={renameNew}
              onChange={(e) => setRenameNew(e.target.value)}
              className={inputClass}
              autoFocus
            />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setShowRename(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg"
            >
              Cancel
            </button>
            <button type="submit" className={btnPrimary}>
              Rename
            </button>
          </div>
        </form>
      </Modal>

      {/* Chmod Modal */}
      <Modal
        isOpen={showChmod}
        onClose={() => setShowChmod(false)}
        title={`Permissions — ${chmodTarget?.name || ""}`}
        size="sm"
      >
        <form onSubmit={handleChmodSubmit} className="space-y-4">
          <div>
            <label className={labelClass}>Octal *</label>
            <input
              type="text"
              required
              maxLength={4}
              value={chmodValue}
              onChange={(e) => setChmodValue(e.target.value.replace(/[^0-7]/g, ""))}
              className={`${inputClass} font-mono`}
              placeholder="644"
              autoFocus
            />
            <ChmodPreview value={chmodValue} />
          </div>
          {chmodTarget?.type === "directory" && (
            <label className="flex items-center gap-2 text-sm text-panel-text">
              <input
                type="checkbox"
                checked={chmodRecursive}
                onChange={(e) => setChmodRecursive(e.target.checked)}
                className="accent-blue-500"
              />
              Apply recursively to all contents
            </label>
          )}
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setShowChmod(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg"
            >
              Cancel
            </button>
            <button type="submit" className={btnPrimary}>
              Apply
            </button>
          </div>
        </form>
      </Modal>

      {/* Password Protect Modal */}
      <Modal
        isOpen={showProtect}
        onClose={() => setShowProtect(false)}
        title={`Password Protect — ${protectTarget?.name || ""}`}
        size="sm"
      >
        <form onSubmit={handleProtectSubmit} className="space-y-4">
          <p className="text-xs text-panel-muted">
            Adds HTTP Basic Auth via <code className="text-panel-text font-mono">.htaccess</code> +
            <code className="text-panel-text font-mono"> .htpasswd</code>. Requires an Apache-enabled
            vhost or nginx with the <code className="text-panel-text font-mono">auth_basic</code> module.
          </p>
          <div>
            <label className={labelClass}>Username *</label>
            <input
              type="text"
              required
              value={protectForm.username}
              onChange={(e) => setProtectForm({ ...protectForm, username: e.target.value })}
              className={inputClass}
              autoFocus
            />
          </div>
          <div>
            <label className={labelClass}>Password</label>
            <input
              type="password"
              value={protectForm.password}
              onChange={(e) => setProtectForm({ ...protectForm, password: e.target.value })}
              className={inputClass}
              placeholder="Leave blank to keep existing"
            />
          </div>
          <div>
            <label className={labelClass}>Realm label</label>
            <input
              type="text"
              value={protectForm.label}
              onChange={(e) => setProtectForm({ ...protectForm, label: e.target.value })}
              className={inputClass}
              placeholder="Restricted Area"
            />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setShowProtect(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg"
            >
              Cancel
            </button>
            <button type="submit" className={btnPrimary}>
              <KeyRound size={14} />
              Protect
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

function ChmodPreview({ value }: { value: string }) {
  const padded = value.padStart(3, "0").slice(-3);
  const rwx = (d: string) => {
    const n = parseInt(d, 10) || 0;
    return `${n & 4 ? "r" : "-"}${n & 2 ? "w" : "-"}${n & 1 ? "x" : "-"}`;
  };
  return (
    <div className="mt-2 flex items-center gap-2 text-xs font-mono">
      <span className="text-panel-muted">Owner: </span>
      <code className="text-panel-text">{rwx(padded[0])}</code>
      <span className="text-panel-muted">Group: </span>
      <code className="text-panel-text">{rwx(padded[1])}</code>
      <span className="text-panel-muted">Other: </span>
      <code className="text-panel-text">{rwx(padded[2])}</code>
    </div>
  );
}
