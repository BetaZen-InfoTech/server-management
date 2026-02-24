import { useState, useEffect } from "react";
import { Card, Button, Table } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  FolderOpen, File, Upload, FolderPlus, RefreshCw, Trash2, Download,
  ChevronRight, Home, ArrowUp, Edit
} from "lucide-react";

interface FileItem {
  name: string;
  type: "file" | "directory";
  size: string;
  permissions: string;
  modified: string;
  path: string;
}

export default function FilesPage() {
  const [files, setFiles] = useState<FileItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [currentPath, setCurrentPath] = useState("/var/www");

  useEffect(() => {
    fetchFiles();
  }, [currentPath]);

  const fetchFiles = async () => {
    setLoading(true);
    try {
      const res = await api.get("/files", { params: { path: currentPath } });
      setFiles(res.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const navigateTo = (path: string) => {
    setCurrentPath(path);
  };

  const navigateUp = () => {
    const parts = currentPath.split("/").filter(Boolean);
    if (parts.length > 1) {
      parts.pop();
      setCurrentPath("/" + parts.join("/"));
    }
  };

  const handleDelete = async (item: FileItem) => {
    if (!confirm(`Are you sure you want to delete "${item.name}"?`)) return;
    try {
      await api.delete("/files", { data: { path: item.path } });
      toast.success(`${item.name} deleted`);
      fetchFiles();
    } catch {
      toast.error("Failed to delete item");
    }
  };

  const breadcrumbs = currentPath.split("/").filter(Boolean);

  const columns = [
    {
      header: "Name",
      accessor: (f: FileItem) => (
        <button
          onClick={() => f.type === "directory" ? navigateTo(f.path) : undefined}
          className={`flex items-center gap-2 ${f.type === "directory" ? "cursor-pointer hover:text-blue-400" : ""}`}
        >
          {f.type === "directory" ? (
            <FolderOpen size={16} className="text-yellow-400 shrink-0" />
          ) : (
            <File size={16} className="text-panel-muted shrink-0" />
          )}
          <span className={`font-medium ${f.type === "directory" ? "text-panel-text" : "text-panel-muted"}`}>
            {f.name}
          </span>
        </button>
      ),
    },
    {
      header: "Size",
      accessor: (f: FileItem) => (
        <span className="text-panel-muted text-sm">
          {f.type === "directory" ? "--" : f.size}
        </span>
      ),
    },
    {
      header: "Permissions",
      accessor: (f: FileItem) => (
        <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">
          {f.permissions}
        </code>
      ),
    },
    {
      header: "Modified",
      accessor: (f: FileItem) => (
        <span className="text-panel-muted text-sm">{f.modified}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (f: FileItem) => (
        <div className="flex items-center gap-1">
          {f.type === "file" && (
            <>
              <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="Edit">
                <Edit size={14} />
              </button>
              <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors" title="Download">
                <Download size={14} />
              </button>
            </>
          )}
          <button
            onClick={() => handleDelete(f)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
            title="Delete"
          >
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">File Manager</h1>
          <p className="text-panel-muted text-sm mt-1">
            Browse and manage server files
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchFiles}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => toast("New Folder modal coming soon")}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <FolderPlus size={14} />
            New Folder
          </Button>
          <Button
            onClick={() => toast("Upload modal coming soon")}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Upload size={14} />
            Upload
          </Button>
        </div>
      </div>

      {/* Breadcrumb Navigation */}
      <Card>
        <div className="p-3 flex items-center gap-1 overflow-x-auto">
          <button
            onClick={() => navigateTo("/")}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-panel-text transition-colors shrink-0"
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
      </Card>

      {/* File List */}
      <Card>
        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2, 3, 4, 5, 6].map((i) => (
                <div key={i} className="h-10 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : files.length > 0 ? (
          <Table columns={columns} data={files} />
        ) : (
          <div className="text-center py-16 px-4">
            <FolderOpen size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">Empty directory</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              This directory is empty. Upload files or create a new folder.
            </p>
            <div className="flex items-center justify-center gap-2">
              <Button
                onClick={() => toast("Upload modal coming soon")}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Upload size={14} />
                Upload Files
              </Button>
              <Button
                onClick={() => toast("New Folder modal coming soon")}
                className="inline-flex items-center gap-2 px-4 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm font-medium transition-colors"
              >
                <FolderPlus size={14} />
                New Folder
              </Button>
            </div>
          </div>
        )}
      </Card>
    </div>
  );
}
