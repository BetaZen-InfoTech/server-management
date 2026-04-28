import { useState, useEffect, useMemo, useRef, useCallback } from "react";
import { useSearchParams } from "react-router-dom";
import { Card, Button, Modal, PasswordInput, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { useAuthStore } from "@/store/auth";
import {
  FolderOpen, File as FileIcon, Upload, FolderPlus, RefreshCw, Trash2, Download,
  ChevronRight, Home, ArrowUp, Edit, FilePlus, Archive, FileArchive, Copy,
  Scissors, Lock, Eye, EyeOff, Search, X, Check, MoreHorizontal,
  ArrowLeft, ArrowRight, KeyRound, FileText, RotateCcw, AlertTriangle
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
  // Role-aware default + jail. Admins (vendor_owner / explicit
  // server.manage permission) land at /home/ and roam freely. Every
  // other role is jailed to /home/<their-username>/ — they can't ascend
  // past it via navigateUp(), breadcrumbs, or a typed path. Backend
  // enforces the same rule (defense in depth) so a hand-crafted API
  // call also gets 403.
  const authUser = useAuthStore((s) => s.user);
  const isAdmin =
    authUser?.role === "vendor_owner" ||
    authUser?.role === "admin" ||
    (authUser?.permissions?.includes("server.manage") ?? false);
  const ownUsername = authUser?.username || "";
  const jailRoot = !isAdmin && ownUsername ? `/home/${ownUsername}` : "";
  const defaultPath = isAdmin ? "/home" : (jailRoot || "/home");
  const initialPath = searchParams.get("path") || defaultPath;

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
  // Refs for the editor's textarea + the gutter that shows line numbers.
  // Scroll sync happens via a CSS transform on the gutter <pre>, driven
  // by the textarea's onScroll. transform is more reliable than
  // mutating scrollTop on an overflow-hidden element across browsers.
  const editorTextareaRef = useRef<HTMLTextAreaElement>(null);
  const editorGutterRef = useRef<HTMLPreElement>(null);
  // Editor UX state:
  //   editorScrollTop — pixel offset the gutter translates by to stay
  //     aligned with the textarea's scroll.
  //   currentLine — 1-indexed cursor line; gutter uses it to highlight.
  //   originalContent — the server-loaded content; compared against
  //     fileContent to detect unsaved changes and flag the title.
  //   wordWrap — toggleable; default false so line numbers always map
  //     1:1 to logical lines.
  //   gotoLineValue — controlled input for the "Go to line" jumper.
  const [editorScrollTop, setEditorScrollTop] = useState(0);
  const [currentLine, setCurrentLine] = useState(1);
  const [originalContent, setOriginalContent] = useState("");
  const [wordWrap, setWordWrap] = useState(false);
  const [gotoLineOpen, setGotoLineOpen] = useState(false);
  const [gotoLineValue, setGotoLineValue] = useState("");
  const [archiveName, setArchiveName] = useState("");
  const [archiveFormat, setArchiveFormat] = useState<"zip" | "tar.gz">("zip");
  const [destPath, setDestPath] = useState("");
  const [renameTarget, setRenameTarget] = useState<FileItem | null>(null);
  const [renameNew, setRenameNew] = useState("");
  // chmodTargets is an array so the same modal powers single-item
  // right-click and multi-select bulk chmod. Single-item keeps the
  // filename in the title; multi-item shows the count.
  const [chmodTargets, setChmodTargets] = useState<FileItem[]>([]);
  const [chmodValue, setChmodValue] = useState("644");
  const [chmodRecursive, setChmodRecursive] = useState(false);

  // Context menu — `targets` carries the list of files the menu acts on.
  // When right-click lands on a file that's part of the current multi-
  // selection, the menu applies to the whole selection. Otherwise it
  // narrows to just the clicked file (and snaps the selection to match,
  // so the user can see what "Delete" is about to target).
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; targets: FileItem[] } | null>(null);

  // Operation progress — compress/extract/bulk chmod aren't streaming,
  // but they can take several seconds on large trees. Rather than a
  // frozen UI + delayed toast, show a modal with the operation label +
  // elapsed time so the user knows work is happening.
  const [busyOp, setBusyOp] = useState<{ label: string; started: number } | null>(null);
  const [busyNow, setBusyNow] = useState(0);

  // Delete dialog — replaces the generic confirmAction for files because
  // we need a "Delete permanently" checkbox inside the prompt. Items
  // carries the batch being acted on (1 item for single-delete,
  // selectedFiles for bulk).
  const [deleteDialog, setDeleteDialog] = useState<{ items: FileItem[] } | null>(null);
  const [deletePermanent, setDeletePermanent] = useState(false);
  const [deleting, setDeleting] = useState(false);

  // Trash drawer state — a separate panel the user opens from the
  // header to browse, restore, or permanently delete previously-soft-
  // deleted items. Contents are fetched lazily when the drawer opens.
  interface TrashItem {
    id: string;
    original_path: string;
    original_name: string;
    deleted_at: string;
    size: number;
    size_human: string;
    kind: "file" | "directory";
  }
  const [showTrash, setShowTrash] = useState(false);
  const [trashItems, setTrashItems] = useState<TrashItem[]>([]);
  const [trashLoading, setTrashLoading] = useState(false);
  const [trashBusy, setTrashBusy] = useState<string | null>(null);

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

  // Tick the elapsed-time counter for the busy overlay while an op runs.
  // 500ms is fine-grained enough to feel alive without thrashing React.
  useEffect(() => {
    if (!busyOp) return;
    const iv = setInterval(() => setBusyNow(Date.now()), 500);
    return () => clearInterval(iv);
  }, [busyOp]);

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

  // isWithinJail returns true when path is at or below jailRoot. Empty
  // jailRoot (admins) means everything is allowed.
  const isWithinJail = (path: string): boolean => {
    if (!jailRoot) return true;
    const normalized = path.replace(/\/+$/, "");
    const root = jailRoot.replace(/\/+$/, "");
    return normalized === root || normalized.startsWith(root + "/");
  };
  const navigateTo = (path: string) => {
    if (path === currentPath) return;
    if (!isWithinJail(path)) {
      toast.error(`You can only browse inside ${jailRoot}/`);
      return;
    }
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
      const next = "/" + parts.join("/");
      if (!isWithinJail(next || "/")) {
        toast.error(`You're at the top of your home folder (${jailRoot})`);
        return;
      }
      navigateTo(next);
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
    // Reset editor UX state so the last file's cursor line / scroll /
    // goto-line panel don't leak into the fresh open.
    setEditorScrollTop(0);
    setCurrentLine(1);
    setGotoLineOpen(false);
    setGotoLineValue("");
    try {
      const res = await api.get("/files/read", { params: { path: item.path } });
      const content = res.data.data?.content ?? "";
      setFileContent(content);
      setOriginalContent(content);
    } catch {
      toast.error("Failed to read file");
      setFileContent("");
      setOriginalContent("");
    } finally {
      setLoadingFile(false);
    }
  };

  // closeEditor centralises the "leave the editor" path so we can warn
  // once before discarding unsaved edits. Wired into the Cancel button,
  // the Modal close (X), and the Escape key.
  const closeEditor = () => {
    if (!editorReadonly && fileContent !== originalContent) {
      const ok = confirm("You have unsaved changes. Discard and close?");
      if (!ok) return;
    }
    setShowEditor(false);
    setEditingFile(null);
    setEditorReadonly(false);
    setFileContent("");
    setOriginalContent("");
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

  const handleSaveFile = async (keepOpen = false) => {
    if (!editingFile) return;
    setSaving(true);
    try {
      await api.put("/files/edit", { path: editingFile.path, content: fileContent });
      toast.success(`${editingFile.name} saved`);
      // Sync the baseline so the dirty indicator clears. Keeps the
      // editor open when the caller wants Save without close (the
      // Ctrl/Cmd-S keybind — matches every editor operators know).
      setOriginalContent(fileContent);
      if (!keepOpen) {
        setShowEditor(false);
        setEditingFile(null);
      }
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to save file");
    } finally {
      setSaving(false);
    }
  };

  // goToLine scrolls the textarea so `line` (1-indexed) is in view and
  // places the cursor at that line's first column. Clamps to the file's
  // actual line count so "jump to 99999" just goes to the bottom
  // instead of acting weird.
  const goToLine = (line: number) => {
    const ta = editorTextareaRef.current;
    if (!ta) return;
    const lines = fileContent.split("\n");
    const target = Math.max(1, Math.min(line, lines.length));
    // Compute character offset for the start of the target line.
    let offset = 0;
    for (let i = 0; i < target - 1; i++) offset += lines[i].length + 1;
    ta.focus();
    ta.selectionStart = ta.selectionEnd = offset;
    // Scroll the target line roughly into the middle of the viewport.
    const approxLineHeight = 22.4; // 1.4rem @ 14px base
    const visibleLines = Math.floor(ta.clientHeight / approxLineHeight);
    const scrollLine = Math.max(0, target - Math.floor(visibleLines / 2));
    ta.scrollTop = scrollLine * approxLineHeight;
    setEditorScrollTop(ta.scrollTop);
    setCurrentLine(target);
    setGotoLineOpen(false);
    setGotoLineValue("");
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

  // Open the custom delete dialog. Replaces the old confirmAction
  // one-shot because we now need a "Delete permanently" checkbox in
  // the prompt — something the shared dialog doesn't support, and
  // factoring that into a generic API wasn't worth the call-site churn.
  const openDeleteDialog = (items: FileItem[]) => {
    if (items.length === 0) return;
    setDeletePermanent(false);
    setDeleteDialog({ items });
  };
  const handleDeleteOne = (item: FileItem) => openDeleteDialog([item]);
  const handleBulkDelete = () => openDeleteDialog(selectedFiles);

  // Confirm handler for the delete dialog. Soft delete (permanent=false)
  // moves items into the user's .Trash; permanent=true is the old
  // rm -rf behaviour. We send one request per item so a single bad
  // path doesn't take the rest down with it.
  const confirmDelete = async () => {
    if (!deleteDialog) return;
    const items = deleteDialog.items;
    setDeleting(true);
    let ok = 0;
    for (const f of items) {
      try {
        await api.delete("/files/delete", { data: { path: f.path, permanent: deletePermanent } });
        ok++;
      } catch (err: any) {
        if (items.length === 1) {
          toast.error(err?.response?.data?.error?.message || "Failed to delete");
        }
      }
    }
    setDeleting(false);
    setDeleteDialog(null);
    if (ok > 0) {
      const verb = deletePermanent ? "deleted" : "moved to Trash";
      toast.success(items.length === 1 ? `${items[0].name} ${verb}` : `${ok}/${items.length} item(s) ${verb}`);
    }
    clearSelection();
    fetchFiles();
  };

  // ── Trash drawer ────────────────────────────────────────
  const fetchTrash = async () => {
    setTrashLoading(true);
    try {
      const res = await api.get("/files/trash");
      setTrashItems(res.data.data || []);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to load Trash");
      setTrashItems([]);
    } finally {
      setTrashLoading(false);
    }
  };

  const openTrash = () => {
    setShowTrash(true);
    fetchTrash();
  };

  const restoreFromTrash = async (item: TrashItem, overwrite = false) => {
    setTrashBusy(item.id);
    try {
      await api.post("/files/trash/restore", { id: item.id, overwrite });
      toast.success(`${item.original_name} restored`);
      fetchTrash();
      fetchFiles();
    } catch (err: any) {
      const msg = err?.response?.data?.error?.message || "Failed to restore";
      // Auto-offer overwrite when the origin is occupied. Only when
      // overwrite wasn't already the attempt — otherwise we'd loop.
      if (!overwrite && /already exists/i.test(msg)) {
        if (await confirmAction({
          title: "Overwrite?",
          description: `Something already exists at ${item.original_path}. Overwrite it with the restored copy?`,
          danger: true,
          confirmLabel: "Overwrite",
        })) {
          return restoreFromTrash(item, true);
        }
      } else {
        toast.error(msg);
      }
    } finally {
      setTrashBusy(null);
    }
  };

  const deleteFromTrash = async (item: TrashItem) => {
    if (!(await confirmAction({
      title: "Delete permanently?",
      description: `Permanently delete "${item.original_name}"? This cannot be undone.`,
      danger: true,
      confirmLabel: "Delete",
    }))) return;
    setTrashBusy(item.id);
    try {
      await api.delete("/files/trash", { data: { id: item.id } });
      toast.success(`${item.original_name} deleted`);
      fetchTrash();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to delete");
    } finally {
      setTrashBusy(null);
    }
  };

  const emptyTrash = async () => {
    if (trashItems.length === 0) return;
    if (!(await confirmAction({
      title: "Empty Trash?",
      description: `Permanently delete all ${trashItems.length} item(s) in Trash? This cannot be undone.`,
      danger: true,
      confirmLabel: "Empty Trash",
    }))) return;
    setTrashLoading(true);
    try {
      await api.delete("/files/trash", { data: { id: "" } });
      toast.success("Trash emptied");
      fetchTrash();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to empty Trash");
    } finally {
      setTrashLoading(false);
    }
  };

  const handleExtract = async (item: FileItem) => {
    if (!await confirmAction({ title: "Extract?", description: `Extract "${item.name}" into ${currentPath}?`, confirmLabel: "Extract" })) return;
    setBusyOp({ label: `Extracting "${item.name}"…`, started: Date.now() });
    try {
      await api.post("/files/extract", { archive: item.path, destination: currentPath });
      toast.success(`${item.name} extracted`);
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to extract");
    } finally {
      setBusyOp(null);
    }
  };

  const handleCompressSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!archiveName.trim()) return toast.error("Enter an archive name");
    if (selectedCount === 0) return toast.error("Select files to compress");
    const ext = archiveFormat === "zip" ? ".zip" : ".tar.gz";
    const name = archiveName.endsWith(ext) ? archiveName : archiveName + ext;
    // Close the form modal before we start — the busy overlay takes
    // over so the user isn't staring at a disabled form with no feedback.
    setShowCompress(false);
    setBusyOp({
      label: selectedCount === 1
        ? `Compressing "${selectedFiles[0].name}" → ${name}…`
        : `Compressing ${selectedCount} items → ${name}…`,
      started: Date.now(),
    });
    try {
      await api.post("/files/compress", {
        paths: selectedFiles.map((f) => f.path),
        output: `${currentPath}/${name}`,
        format: archiveFormat,
      });
      toast.success(`Created ${name}`);
      setArchiveName("");
      clearSelection();
      fetchFiles();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Compression failed");
    } finally {
      setBusyOp(null);
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
    if (chmodTargets.length === 0) return;
    if (!/^[0-7]{3,4}$/.test(chmodValue)) return toast.error("Enter octal (e.g. 755)");
    // Close the modal and show the busy overlay so long bulk-chmods
    // (e.g. 500 files) give the user feedback while we loop requests.
    setShowChmod(false);
    setBusyOp({
      label: chmodTargets.length === 1
        ? `chmod ${chmodValue} "${chmodTargets[0].name}"…`
        : `chmod ${chmodValue} on ${chmodTargets.length} items…`,
      started: Date.now(),
    });
    let ok = 0;
    let lastErr = "";
    for (const t of chmodTargets) {
      try {
        await api.post("/files/chmod", {
          path: t.path,
          permissions: chmodValue,
          recursive: chmodRecursive,
        });
        ok++;
      } catch (err: any) {
        lastErr = err?.response?.data?.error?.message || "Chmod failed";
      }
    }
    setBusyOp(null);
    setChmodTargets([]);
    if (ok === chmodTargets.length) {
      toast.success(ok === 1 ? "Permissions updated" : `Updated ${ok} item(s)`);
    } else if (ok > 0) {
      toast.error(`${ok}/${chmodTargets.length} updated — ${lastErr}`);
    } else {
      toast.error(lastErr || "Chmod failed");
    }
    fetchFiles();
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
          <Button onClick={openTrash} className={btnGhost}>
            <Trash2 size={14} />
            Trash
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
          <button
            onClick={() => {
              // Bulk chmod over the current multi-selection. Mirrors the
              // "Change Permissions" right-click entry but available from
              // the bulk bar for users who never right-click.
              setChmodTargets(selectedFiles);
              const hasDir = selectedFiles.some((t) => t.type === "directory");
              setChmodValue(hasDir ? "755" : "644");
              setChmodRecursive(false);
              setShowChmod(true);
            }}
            className={btnGhost}
          >
            <Lock size={14} />
            Permissions
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
                        // If the user right-clicks a file that's already in
                        // a multi-selection, treat the selection as the
                        // target set — otherwise narrow to just this file
                        // (and snap the selection to match, so "Delete" /
                        // "Permissions" can't silently act on a different
                        // row than the one the user aimed at).
                        const clickedIsSelected = selected.has(f.path);
                        if (clickedIsSelected && selectedCount > 1) {
                          const targets = visibleFiles.filter((x) => selected.has(x.path));
                          setCtxMenu({ x: e.clientX, y: e.clientY, targets });
                        } else {
                          setSelected(new Set([f.path]));
                          setCtxMenu({ x: e.clientX, y: e.clientY, targets: [f] });
                        }
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
                              // Same selection logic as the row's context
                              // menu — clicking More on a row that's in a
                              // multi-selection opens the menu over the
                              // whole selection.
                              const clickedIsSelected = selected.has(f.path);
                              if (clickedIsSelected && selectedCount > 1) {
                                const targets = visibleFiles.filter((x) => selected.has(x.path));
                                setCtxMenu({ x: e.clientX, y: e.clientY, targets });
                              } else {
                                setCtxMenu({ x: e.clientX, y: e.clientY, targets: [f] });
                              }
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
        const targets = ctxMenu.targets;
        const isMulti = targets.length > 1;
        const single = targets[0];
        const close = () => setCtxMenu(null);
        const itemClass = "w-full text-left px-3 py-1.5 text-sm text-panel-text hover:bg-panel-bg flex items-center gap-2";
        // Actions that only make sense for one file at a time (Open, View,
        // Edit, Rename, Extract, Password Protect/Unprotect) hide when
        // `isMulti`. Bulk-safe actions (Compress, Copy, Move, Delete,
        // Change Permissions) always show.
        return (
          <div
            className="fixed z-50 bg-panel-surface border border-panel-border rounded-lg shadow-xl py-1 min-w-[220px]"
            style={{ left, top }}
            onClick={(e) => e.stopPropagation()}
          >
            {isMulti && (
              <div className="px-3 py-1 text-[11px] uppercase tracking-wider text-panel-muted border-b border-panel-border">
                {targets.length} item(s) selected
              </div>
            )}
            {!isMulti && single.type === "directory" && (
              <button onClick={() => { navigateTo(single.path); close(); }} className={itemClass}>
                <FolderOpen size={12} /> Open
              </button>
            )}
            {!isMulti && single.type === "file" && (
              <button onClick={() => { handleDownload(single); close(); }} className={itemClass}>
                <Download size={12} /> Download
              </button>
            )}
            {!isMulti && single.type === "file" && isEditable(single.name) && (
              <button onClick={() => { handleOpenEditor(single, true); close(); }} className={itemClass}>
                <Eye size={12} /> View
              </button>
            )}
            {!isMulti && single.type === "file" && isEditable(single.name) && (
              <button onClick={() => { handleOpenEditor(single); close(); }} className={itemClass}>
                <Edit size={12} /> Edit
              </button>
            )}
            <button
              onClick={() => {
                setSelected(new Set(targets.map((t) => t.path)));
                setDestPath(currentPath);
                setShowMove(true);
                close();
              }}
              className={itemClass}
            >
              <Scissors size={12} /> Move{isMulti ? ` (${targets.length})` : ""}
            </button>
            <button
              onClick={() => {
                setSelected(new Set(targets.map((t) => t.path)));
                setDestPath(currentPath);
                setShowCopy(true);
                close();
              }}
              className={itemClass}
            >
              <Copy size={12} /> Copy{isMulti ? ` (${targets.length})` : ""}
            </button>
            {!isMulti && (
              <button
                onClick={() => {
                  setRenameTarget(single);
                  setRenameNew(single.name);
                  setShowRename(true);
                  close();
                }}
                className={itemClass}
              >
                <Edit size={12} /> Rename
              </button>
            )}
            <button
              onClick={() => {
                setChmodTargets(targets);
                // Default to 755 if any target is a directory, else 644.
                const hasDir = targets.some((t) => t.type === "directory");
                setChmodValue(hasDir ? "755" : "644");
                setChmodRecursive(false);
                setShowChmod(true);
                close();
              }}
              className={itemClass}
            >
              <Lock size={12} /> Change Permissions{isMulti ? ` (${targets.length})` : ""}
            </button>
            <div className="border-t border-panel-border my-1" />
            {!isMulti && single.type === "file" && isArchive(single.name) && (
              <button onClick={() => { handleExtract(single); close(); }} className={itemClass}>
                <FileArchive size={12} /> Extract
              </button>
            )}
            <button
              onClick={() => {
                // Seed selection with the context-menu targets so the
                // Compress modal packs exactly what the user aimed at.
                setSelected(new Set(targets.map((t) => t.path)));
                setArchiveName(isMulti ? "archive.zip" : `${single.name}.zip`);
                setShowCompress(true);
                close();
              }}
              className={itemClass}
            >
              <Archive size={12} /> Compress{isMulti ? ` (${targets.length})` : ""}
            </button>
            {!isMulti && single.type === "directory" && (
              <>
                <div className="border-t border-panel-border my-1" />
                <button
                  onClick={() => {
                    setProtectTarget(single);
                    setProtectForm({ username: "", password: "", label: single.name });
                    setShowProtect(true);
                    close();
                  }}
                  className={itemClass}
                >
                  <KeyRound size={12} /> Password Protect
                </button>
                <button
                  onClick={() => { handleUnprotect(single); close(); }}
                  className={itemClass}
                >
                  <KeyRound size={12} /> Remove Protection
                </button>
              </>
            )}
            <div className="border-t border-panel-border my-1" />
            <button
              onClick={() => { openDeleteDialog(targets); close(); }}
              className="w-full text-left px-3 py-1.5 text-sm text-red-400 hover:bg-panel-bg flex items-center gap-2"
            >
              <Trash2 size={12} /> Delete{isMulti ? ` (${targets.length})` : ""}
            </button>
          </div>
        );
      })()}

      {/* Delete confirmation — bespoke instead of confirmAction so the
          "Delete permanently" checkbox can live inside the prompt. Soft
          delete (default) routes to the per-user Trash; permanent ticks
          the checkbox and calls rm -rf on the backend. Cancel closes
          without touching anything. */}
      <Modal
        isOpen={!!deleteDialog}
        onClose={() => !deleting && setDeleteDialog(null)}
        title="Delete?"
        size="sm"
      >
        {deleteDialog && (
          <div className="space-y-4">
            <div className="flex items-start gap-3">
              <div className="shrink-0 w-10 h-10 rounded-full bg-red-500/10 flex items-center justify-center">
                <AlertTriangle size={18} className="text-red-400" />
              </div>
              <div className="min-w-0 flex-1">
                <p className="text-sm text-panel-text">
                  {deleteDialog.items.length === 1
                    ? <>Delete <span className="font-mono text-panel-text">"{deleteDialog.items[0].name}"</span>?</>
                    : <>Delete {deleteDialog.items.length} item(s)?</>}
                </p>
                <p className="text-xs text-panel-muted mt-1">
                  {deletePermanent
                    ? "This cannot be undone."
                    : "The item(s) will be moved to Trash and can be restored later."}
                </p>
              </div>
            </div>

            <label className={`flex items-start gap-2.5 p-3 rounded-lg border cursor-pointer transition-colors ${
              deletePermanent
                ? "border-red-500/40 bg-red-500/5"
                : "border-panel-border bg-panel-bg hover:border-panel-muted/30"
            }`}>
              <input
                type="checkbox"
                checked={deletePermanent}
                onChange={(e) => setDeletePermanent(e.target.checked)}
                className="mt-0.5 w-4 h-4 rounded border-panel-border bg-panel-bg text-red-500 focus:ring-red-500/40"
              />
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium text-panel-text">Delete permanently</div>
                <div className="text-xs text-panel-muted mt-0.5">
                  Skip Trash. Runs rm -rf on the server — there is no undo.
                </div>
              </div>
            </label>

            <div className="flex items-center justify-end gap-2 pt-2 border-t border-panel-border">
              <button
                type="button"
                disabled={deleting}
                onClick={() => setDeleteDialog(null)}
                className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted hover:text-panel-text hover:bg-panel-bg disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="button"
                disabled={deleting}
                onClick={confirmDelete}
                className={`px-4 py-2 text-sm rounded-lg font-medium text-white flex items-center gap-2 disabled:opacity-50 ${
                  deletePermanent ? "bg-red-600 hover:bg-red-700" : "bg-blue-600 hover:bg-blue-700"
                }`}
              >
                {deleting
                  ? "Working..."
                  : deletePermanent ? "Delete permanently" : "Move to Trash"}
              </button>
            </div>
          </div>
        )}
      </Modal>

      {/* Trash drawer — opens from the Trash button in the header. Lists
          every soft-deleted item with its original path, size, and
          deleted-at timestamp, plus per-row Restore / Delete permanently
          actions and a single Empty Trash at the bottom. */}
      <Modal
        isOpen={showTrash}
        onClose={() => setShowTrash(false)}
        title="Trash"
        size="lg"
      >
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <p className="text-sm text-panel-muted">
              Soft-deleted items land here. Restore puts them back at their
              original path; Delete permanently runs rm -rf.
            </p>
            <button
              type="button"
              onClick={fetchTrash}
              className="text-panel-muted hover:text-panel-text p-1.5 rounded"
              title="Refresh"
            >
              <RefreshCw size={14} className={trashLoading ? "animate-spin" : ""} />
            </button>
          </div>

          {trashLoading ? (
            <div className="py-8 text-center text-panel-muted text-sm">Loading…</div>
          ) : trashItems.length === 0 ? (
            <div className="py-10 text-center">
              <Trash2 size={32} className="text-panel-muted/40 mx-auto mb-2" />
              <p className="text-sm text-panel-muted">Trash is empty</p>
            </div>
          ) : (
            <div className="max-h-[60vh] overflow-y-auto border border-panel-border rounded-lg divide-y divide-panel-border">
              {trashItems.map((it) => {
                const busy = trashBusy === it.id;
                const deletedAt = new Date(it.deleted_at);
                const deletedLabel = isNaN(deletedAt.getTime())
                  ? it.deleted_at
                  : deletedAt.toLocaleString();
                return (
                  <div key={it.id} className="flex items-center gap-3 px-3 py-2.5">
                    {it.kind === "directory"
                      ? <FolderOpen size={16} className="text-blue-400 shrink-0" />
                      : <FileIcon size={16} className="text-panel-muted shrink-0" />}
                    <div className="min-w-0 flex-1">
                      <div className="text-sm font-medium text-panel-text truncate">{it.original_name}</div>
                      <div className="text-[11px] text-panel-muted font-mono truncate">{it.original_path}</div>
                      <div className="text-[10px] text-panel-muted mt-0.5">
                        {it.size_human} · deleted {deletedLabel}
                      </div>
                    </div>
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => restoreFromTrash(it)}
                      className="flex items-center gap-1 px-2.5 py-1.5 text-xs rounded border border-panel-border text-panel-muted hover:text-panel-text hover:bg-panel-bg disabled:opacity-50"
                      title="Restore to original path"
                    >
                      <RotateCcw size={12} /> Restore
                    </button>
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => deleteFromTrash(it)}
                      className="flex items-center gap-1 px-2.5 py-1.5 text-xs rounded border border-red-500/30 text-red-400 hover:bg-red-500/10 disabled:opacity-50"
                      title="Delete permanently"
                    >
                      <Trash2 size={12} /> Delete
                    </button>
                  </div>
                );
              })}
            </div>
          )}

          <div className="flex items-center justify-between pt-2 border-t border-panel-border">
            <div className="text-xs text-panel-muted">
              {trashItems.length} item(s)
            </div>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => setShowTrash(false)}
                className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted hover:text-panel-text"
              >
                Close
              </button>
              <button
                type="button"
                disabled={trashItems.length === 0 || trashLoading}
                onClick={emptyTrash}
                className="px-4 py-2 text-sm rounded-lg font-medium text-white bg-red-600 hover:bg-red-700 disabled:opacity-40 disabled:cursor-not-allowed flex items-center gap-2"
              >
                <Trash2 size={14} />
                Empty Trash
              </button>
            </div>
          </div>
        </div>
      </Modal>

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

      {/*
        Editor Modal — rewritten to fix a cluster of bugs:
          • Gutter <pre> no longer inflates the flex parent to `lineCount
            * line-height` because it's absolutely positioned. Previously,
            on a 364-line file, the container ballooned past 8000px.
          • wrap="off" on the textarea so one logical line = one visual
            line, which is what keeps gutter numbers aligned. Operators
            who want wrapping can toggle it from the footer.
          • Scroll sync uses CSS transform on the gutter instead of
            mutating scrollTop on an overflow-hidden element (unreliable
            across browsers).
          • Font sizes match (text-sm + text-sm instead of text-xs gutter
            + text-sm body) so line boxes line up at every row.
        Plus new UX: dirty indicator, word-wrap toggle, go-to-line,
        current-line highlight, unsaved close-confirm.
      */}
      <Modal
        isOpen={showEditor}
        onClose={closeEditor}
        title={`${editorReadonly ? "View" : "Edit"} — ${editingFile?.name || ""}${
          !editorReadonly && fileContent !== originalContent ? " •" : ""
        }`}
        size="xl"
        fillBody
        footer={
          <div className="flex items-center justify-between gap-3 flex-wrap">
            <div className="flex items-center gap-3 text-xs text-panel-muted">
              <span className="flex items-center gap-1">
                <span className={`w-1.5 h-1.5 rounded-full ${
                  !editorReadonly && fileContent !== originalContent ? "bg-amber-400" : "bg-green-400"
                }`} />
                {!editorReadonly && fileContent !== originalContent ? "unsaved" : "saved"}
              </span>
              <span>
                Ln <span className="text-panel-text font-mono">{currentLine}</span>
                {" / "}
                <span className="font-mono">{lineCount}</span>
              </span>
              <span className="font-mono">{editorLang || "text"}</span>
              <label className="flex items-center gap-1.5 cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={wordWrap}
                  onChange={(e) => setWordWrap(e.target.checked)}
                  className="w-3 h-3"
                />
                Word wrap
              </label>
              <button
                type="button"
                onClick={() => setGotoLineOpen(true)}
                className="text-blue-400 hover:text-blue-300 transition-colors"
                title="Go to line (Ctrl/Cmd-G)"
              >
                Go to line
              </button>
            </div>
            <div className="flex gap-3">
              <button
                type="button"
                onClick={closeEditor}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg"
              >
                {editorReadonly ? "Close" : "Cancel"}
              </button>
              {!editorReadonly && (
                <button
                  onClick={() => handleSaveFile(false)}
                  disabled={saving || loadingFile || fileContent === originalContent}
                  className={`${btnPrimary} disabled:opacity-40 disabled:cursor-not-allowed`}
                  title={fileContent === originalContent ? "No changes" : "Save (Ctrl/Cmd-S to save without closing)"}
                >
                  <Edit size={14} />
                  {saving ? "Saving…" : "Save"}
                </button>
              )}
            </div>
          </div>
        }
      >
        <div className="h-full flex flex-col gap-3">
          {loadingFile ? (
            <div className="flex-1 bg-panel-border/20 rounded animate-pulse" />
          ) : (
            <>
              <div className="flex items-center justify-between text-xs shrink-0 gap-3">
                <code className="text-panel-muted font-mono truncate">{editingFile?.path}</code>
                <span className="text-panel-muted uppercase tracking-wider shrink-0">
                  {editorLang} · {lineCount} lines · {new Blob([fileContent]).size} bytes
                </span>
              </div>

              {/* Go-to-line jumper — slides in when opened. Separate
                  form so Enter in the input triggers jump without
                  submitting any outer form. */}
              {gotoLineOpen && !editorReadonly && (
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    const n = parseInt(gotoLineValue, 10);
                    if (Number.isFinite(n)) goToLine(n);
                  }}
                  className="flex items-center gap-2 shrink-0 bg-blue-500/5 border border-blue-500/20 rounded-lg px-3 py-2"
                >
                  <label className="text-xs text-panel-muted">Go to line:</label>
                  <input
                    type="number"
                    autoFocus
                    min={1}
                    max={lineCount}
                    value={gotoLineValue}
                    onChange={(e) => setGotoLineValue(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Escape") setGotoLineOpen(false);
                    }}
                    className="w-24 px-2 py-1 text-xs bg-panel-bg border border-panel-border rounded text-panel-text font-mono focus:outline-none focus:ring-1 focus:ring-blue-500/40"
                    placeholder={`1–${lineCount}`}
                  />
                  <button type="submit" className="text-xs px-2 py-1 bg-blue-600 hover:bg-blue-700 text-white rounded">
                    Jump
                  </button>
                  <button
                    type="button"
                    onClick={() => setGotoLineOpen(false)}
                    className="text-xs px-2 py-1 text-panel-muted hover:text-panel-text"
                  >
                    Cancel
                  </button>
                </form>
              )}

              {/*
                Absolute-positioned layout:
                  • parent is `relative` + flex-1 so it takes the
                    remaining vertical room from the modal body.
                  • Gutter is absolutely positioned on the left, its
                    height is driven by the parent (not by the 364 line
                    numbers inside). The inner <pre> is translateY'd to
                    match the textarea's scroll.
                  • Textarea is absolutely positioned on the right with
                    wrap=off so logical lines = visual lines.
              */}
              <div className="flex-1 relative border border-panel-border rounded-lg overflow-hidden bg-panel-bg min-h-[320px]">
                <div className="absolute inset-y-0 left-0 w-14 overflow-hidden border-r border-panel-border bg-panel-bg/60 pointer-events-none">
                  <pre
                    ref={editorGutterRef}
                    aria-hidden="true"
                    className="py-2 px-2 text-right text-sm font-mono select-none leading-[1.5rem] whitespace-pre"
                    style={{ transform: `translateY(-${editorScrollTop}px)` }}
                  >
                    {Array.from({ length: lineCount }, (_, i) => (
                      <div
                        key={i}
                        className={
                          i + 1 === currentLine
                            ? "text-blue-400 font-semibold"
                            : "text-panel-muted/50"
                        }
                      >
                        {i + 1}
                      </div>
                    ))}
                  </pre>
                </div>
                <textarea
                  ref={editorTextareaRef}
                  value={fileContent}
                  readOnly={editorReadonly}
                  wrap={wordWrap ? "soft" : "off"}
                  onChange={(e) => setFileContent(e.target.value)}
                  onScroll={(e) => setEditorScrollTop(e.currentTarget.scrollTop)}
                  onSelect={(e) => {
                    // Update the cursor-line indicator + highlight. Cheap:
                    // count newlines up to the caret offset.
                    const pos = e.currentTarget.selectionStart;
                    const before = fileContent.substring(0, pos);
                    setCurrentLine(before.split("\n").length);
                  }}
                  onKeyDown={(e) => {
                    if (!editorReadonly && e.key === "Tab") {
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
                    // Ctrl/Cmd-S → save without closing so operators
                    // can iterate.
                    if (!editorReadonly && (e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
                      e.preventDefault();
                      if (!saving && !loadingFile && fileContent !== originalContent) {
                        handleSaveFile(true);
                      }
                    }
                    // Ctrl/Cmd-G → go-to-line jumper.
                    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "g") {
                      e.preventDefault();
                      setGotoLineOpen(true);
                    }
                    // Escape closes the go-to-line box if open.
                    if (e.key === "Escape" && gotoLineOpen) {
                      e.preventDefault();
                      setGotoLineOpen(false);
                    }
                  }}
                  className="absolute inset-y-0 left-14 right-0 py-2 px-3 bg-panel-bg text-panel-text font-mono text-sm focus:outline-none resize-none leading-[1.5rem]"
                  style={{ whiteSpace: wordWrap ? "pre-wrap" : "pre" }}
                  spellCheck={false}
                />
              </div>
            </>
          )}
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

      {/* Busy overlay — shown while a non-streaming long op (compress,
          extract, bulk chmod) is in flight. The backend doesn't stream
          progress for these, so we show the operation label, an
          indeterminate bar, and elapsed seconds so the user knows it
          hasn't hung. Cannot be dismissed — closes itself when the op
          finishes (success or error). */}
      {busyOp && (() => {
        const elapsed = Math.max(0, Math.floor((busyNow || Date.now()) - busyOp.started) / 1000);
        return (
          <div className="fixed inset-0 z-[60] bg-black/60 backdrop-blur-sm flex items-center justify-center p-4">
            <div className="bg-panel-surface border border-panel-border rounded-lg shadow-2xl w-full max-w-sm p-5">
              <div className="flex items-center gap-3 mb-3">
                <RefreshCw size={18} className="text-blue-400 animate-spin shrink-0" />
                <div className="text-sm font-medium text-panel-text">Working…</div>
                <div className="ml-auto text-xs text-panel-muted font-mono">
                  {elapsed.toFixed(1)}s
                </div>
              </div>
              <p className="text-sm text-panel-muted break-words">{busyOp.label}</p>
              <div className="mt-4 h-1 bg-panel-bg rounded overflow-hidden relative">
                <div className="absolute inset-y-0 w-1/3 bg-blue-500 animate-[slide_1.2s_ease-in-out_infinite]" style={{
                  animation: "slide 1.2s ease-in-out infinite",
                }} />
              </div>
              <style>{`@keyframes slide { 0% { left: -40%; } 100% { left: 100%; } }`}</style>
            </div>
          </div>
        );
      })()}

      {/* Chmod Modal — single-item mode shows the filename in the title,
          multi-item mode shows the count + lists the first few paths so
          the user can sanity-check what they're about to chmod. */}
      <Modal
        isOpen={showChmod}
        onClose={() => setShowChmod(false)}
        title={
          chmodTargets.length === 1
            ? `Permissions — ${chmodTargets[0].name}`
            : `Permissions — ${chmodTargets.length} items`
        }
        size="sm"
      >
        <form onSubmit={handleChmodSubmit} className="space-y-4">
          {chmodTargets.length > 1 && (
            <div className="max-h-28 overflow-y-auto border border-panel-border rounded-lg bg-panel-bg p-2 text-[11px] font-mono text-panel-muted space-y-0.5">
              {chmodTargets.slice(0, 50).map((t) => (
                <div key={t.path} className="truncate">{t.path}</div>
              ))}
              {chmodTargets.length > 50 && (
                <div className="text-panel-muted/60">…and {chmodTargets.length - 50} more</div>
              )}
            </div>
          )}
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
          {chmodTargets.some((t) => t.type === "directory") && (
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
            <PasswordInput
              value={protectForm.password}
              onChange={(v) => setProtectForm({ ...protectForm, password: v })}
              inputClassName={inputClass}
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
