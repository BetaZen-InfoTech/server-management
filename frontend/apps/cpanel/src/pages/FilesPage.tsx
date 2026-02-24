import React, { useEffect, useState } from "react";
import { Card, Button, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  FolderOpen,
  File,
  Folder,
  Upload,
  Download,
  Trash2,
  Plus,
  ChevronRight,
  Home,
  ArrowUp,
  Pencil,
  Eye,
  RefreshCw,
} from "lucide-react";

interface FileEntry {
  name: string;
  type: "file" | "directory";
  size: string;
  modified: string;
  permissions: string;
}

export default function FilesPage() {
  const [currentPath, setCurrentPath] = useState("/home/user/public_html");
  const [files, setFiles] = useState<FileEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [showNewFolder, setShowNewFolder] = useState(false);
  const [showNewFile, setShowNewFile] = useState(false);
  const [showRename, setShowRename] = useState(false);
  const [selectedFile, setSelectedFile] = useState<FileEntry | null>(null);
  const [newName, setNewName] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const fetchFiles = async (path: string) => {
    setLoading(true);
    try {
      const res = await api.get("/files", { params: { path } });
      setFiles(res.data.data || []);
    } catch {
      toast.error("Failed to load files");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchFiles(currentPath);
  }, [currentPath]);

  const navigateTo = (name: string) => {
    const newPath =
      currentPath === "/" ? `/${name}` : `${currentPath}/${name}`;
    setCurrentPath(newPath);
  };

  const navigateUp = () => {
    const parts = currentPath.split("/").filter(Boolean);
    if (parts.length > 0) {
      parts.pop();
      setCurrentPath(parts.length === 0 ? "/" : `/${parts.join("/")}`);
    }
  };

  const breadcrumbs = currentPath
    .split("/")
    .filter(Boolean)
    .map((part, idx, arr) => ({
      name: part,
      path: `/${arr.slice(0, idx + 1).join("/")}`,
    }));

  const handleCreateFolder = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newName.trim()) {
      toast.error("Please enter a folder name");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/files/directory", { path: currentPath, name: newName });
      toast.success("Folder created");
      setShowNewFolder(false);
      setNewName("");
      fetchFiles(currentPath);
    } catch (err: any) {
      toast.error(err.response?.data?.message || "Failed to create folder");
    } finally {
      setSubmitting(false);
    }
  };

  const handleCreateFile = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newName.trim()) {
      toast.error("Please enter a file name");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/files/file", { path: currentPath, name: newName });
      toast.success("File created");
      setShowNewFile(false);
      setNewName("");
      fetchFiles(currentPath);
    } catch (err: any) {
      toast.error(err.response?.data?.message || "Failed to create file");
    } finally {
      setSubmitting(false);
    }
  };

  const handleRename = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedFile || !newName.trim()) return;
    setSubmitting(true);
    try {
      await api.put("/files/rename", {
        path: currentPath,
        oldName: selectedFile.name,
        newName,
      });
      toast.success("Renamed successfully");
      setShowRename(false);
      setSelectedFile(null);
      setNewName("");
      fetchFiles(currentPath);
    } catch (err: any) {
      toast.error(err.response?.data?.message || "Failed to rename");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (file: FileEntry) => {
    if (!confirm(`Delete "${file.name}"?`)) return;
    try {
      await api.delete("/files", {
        data: { path: currentPath, name: file.name },
      });
      toast.success("Deleted successfully");
      fetchFiles(currentPath);
    } catch {
      toast.error("Failed to delete");
    }
  };

  const handleDownload = async (file: FileEntry) => {
    try {
      const res = await api.get("/files/download", {
        params: { path: `${currentPath}/${file.name}` },
      });
      if (res.data.data?.url) window.open(res.data.data.url, "_blank");
      toast.success("Download started");
    } catch {
      toast.error("Failed to download file");
    }
  };

  const openRename = (file: FileEntry) => {
    setSelectedFile(file);
    setNewName(file.name);
    setShowRename(true);
  };

  const sortedFiles = [...files].sort((a, b) => {
    if (a.type === "directory" && b.type !== "directory") return -1;
    if (a.type !== "directory" && b.type === "directory") return 1;
    return a.name.localeCompare(b.name);
  });

  return (
    <div className="space-y-6">
      <Card
        title="File Manager"
        description="Browse and manage your files"
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => fetchFiles(currentPath)}
            >
              <RefreshCw size={16} />
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                setNewName("");
                setShowNewFile(true);
              }}
            >
              <File size={16} className="mr-1" /> New File
            </Button>
            <Button
              size="sm"
              onClick={() => {
                setNewName("");
                setShowNewFolder(true);
              }}
            >
              <Plus size={16} className="mr-1" /> New Folder
            </Button>
          </div>
        }
      >
        {/* Breadcrumb */}
        <div className="flex items-center gap-1 mb-4 text-sm overflow-x-auto pb-2">
          <button
            onClick={() => setCurrentPath("/")}
            className="text-panel-muted hover:text-brand-400 transition-colors flex-shrink-0"
          >
            <Home size={16} />
          </button>
          {breadcrumbs.map((bc, idx) => (
            <React.Fragment key={bc.path}>
              <ChevronRight
                size={14}
                className="text-panel-muted flex-shrink-0"
              />
              <button
                onClick={() => setCurrentPath(bc.path)}
                className={`flex-shrink-0 ${
                  idx === breadcrumbs.length - 1
                    ? "text-white font-medium"
                    : "text-panel-muted hover:text-brand-400"
                } transition-colors`}
              >
                {bc.name}
              </button>
            </React.Fragment>
          ))}
        </div>

        {/* File List */}
        {loading ? (
          <div className="flex items-center justify-center py-12">
            <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-brand-400" />
          </div>
        ) : (
          <div className="border border-panel-border rounded-lg overflow-hidden">
            {/* Header */}
            <div className="grid grid-cols-12 gap-4 px-4 py-2 bg-panel-surface text-xs text-panel-muted uppercase font-medium">
              <div className="col-span-5">Name</div>
              <div className="col-span-2">Size</div>
              <div className="col-span-2">Modified</div>
              <div className="col-span-1">Permissions</div>
              <div className="col-span-2 text-right">Actions</div>
            </div>

            {/* Go up */}
            {currentPath !== "/" && (
              <button
                onClick={navigateUp}
                className="w-full grid grid-cols-12 gap-4 px-4 py-2.5 hover:bg-panel-surface/50 transition-colors text-left"
              >
                <div className="col-span-5 flex items-center gap-2 text-sm">
                  <ArrowUp size={16} className="text-panel-muted" />
                  <span className="text-panel-muted">..</span>
                </div>
                <div className="col-span-7" />
              </button>
            )}

            {/* Files */}
            {sortedFiles.length === 0 ? (
              <div className="text-center py-12">
                <FolderOpen
                  size={40}
                  className="mx-auto text-panel-muted mb-3"
                />
                <p className="text-panel-muted text-sm">
                  This directory is empty
                </p>
              </div>
            ) : (
              sortedFiles.map((file) => (
                <div
                  key={file.name}
                  className="grid grid-cols-12 gap-4 px-4 py-2.5 hover:bg-panel-surface/50 transition-colors items-center border-t border-panel-border"
                >
                  <div className="col-span-5">
                    {file.type === "directory" ? (
                      <button
                        onClick={() => navigateTo(file.name)}
                        className="flex items-center gap-2 text-sm hover:text-brand-400 transition-colors"
                      >
                        <Folder size={16} className="text-yellow-400" />
                        <span className="font-medium text-white">
                          {file.name}
                        </span>
                      </button>
                    ) : (
                      <div className="flex items-center gap-2 text-sm">
                        <File size={16} className="text-panel-muted" />
                        <span className="text-panel-text">{file.name}</span>
                      </div>
                    )}
                  </div>
                  <div className="col-span-2 text-sm text-panel-muted">
                    {file.type === "directory" ? "-" : file.size}
                  </div>
                  <div className="col-span-2 text-sm text-panel-muted">
                    {file.modified}
                  </div>
                  <div className="col-span-1">
                    <span className="font-mono text-xs text-panel-muted">
                      {file.permissions}
                    </span>
                  </div>
                  <div className="col-span-2 flex items-center gap-1.5 justify-end">
                    {file.type === "file" && (
                      <>
                        <button
                          onClick={() => handleDownload(file)}
                          className="text-panel-muted hover:text-brand-400 transition-colors"
                          title="Download"
                        >
                          <Download size={14} />
                        </button>
                        <button
                          className="text-panel-muted hover:text-brand-400 transition-colors"
                          title="View"
                        >
                          <Eye size={14} />
                        </button>
                      </>
                    )}
                    <button
                      onClick={() => openRename(file)}
                      className="text-panel-muted hover:text-brand-400 transition-colors"
                      title="Rename"
                    >
                      <Pencil size={14} />
                    </button>
                    <button
                      onClick={() => handleDelete(file)}
                      className="text-panel-muted hover:text-red-400 transition-colors"
                      title="Delete"
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                </div>
              ))
            )}
          </div>
        )}
      </Card>

      {/* New Folder Modal */}
      <Modal
        isOpen={showNewFolder}
        onClose={() => setShowNewFolder(false)}
        title="New Folder"
        size="sm"
      >
        <form onSubmit={handleCreateFolder} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Folder Name
            </label>
            <input
              type="text"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="new-folder"
              autoFocus
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div className="flex justify-end gap-3">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowNewFolder(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Create Folder
            </Button>
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
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              File Name
            </label>
            <input
              type="text"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="index.html"
              autoFocus
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div className="flex justify-end gap-3">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowNewFile(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Create File
            </Button>
          </div>
        </form>
      </Modal>

      {/* Rename Modal */}
      <Modal
        isOpen={showRename}
        onClose={() => {
          setShowRename(false);
          setSelectedFile(null);
        }}
        title="Rename"
        size="sm"
      >
        <form onSubmit={handleRename} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              New Name
            </label>
            <input
              type="text"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              autoFocus
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div className="flex justify-end gap-3">
            <Button
              variant="secondary"
              type="button"
              onClick={() => {
                setShowRename(false);
                setSelectedFile(null);
              }}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Rename
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
