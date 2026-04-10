import { useState, useEffect } from "react";
import { Card, Button, CodeBlock } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Settings, RefreshCw, Save, Globe, Code, Database, FileText, Edit } from "lucide-react";

type ConfigSection = "nginx" | "php" | "mongodb";

interface ConfigData {
  nginx: string;
  php: string;
  mongodb: string;
}

export default function ConfigPage() {
  const [activeSection, setActiveSection] = useState<ConfigSection>("nginx");
  const [configs, setConfigs] = useState<ConfigData>({
    nginx: "",
    php: "",
    mongodb: "",
  });
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState(false);
  const [editContent, setEditContent] = useState("");

  useEffect(() => {
    fetchConfig();
  }, [activeSection]);

  const fetchConfig = async () => {
    setLoading(true);
    try {
      const res = await api.get(`/config/${activeSection}`);
      setConfigs((prev) => ({ ...prev, [activeSection]: res.data.data?.content || "" }));
    } catch {
      setConfigs((prev) => ({ ...prev, [activeSection]: "" }));
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async () => {
    try {
      await api.put(`/config/${activeSection}`, { content: editContent });
      setConfigs((prev) => ({ ...prev, [activeSection]: editContent }));
      setEditing(false);
      toast.success("Configuration saved successfully");
    } catch {
      toast.error("Failed to save configuration");
    }
  };

  const handleEdit = () => {
    setEditContent(configs[activeSection]);
    setEditing(true);
  };

  const sections: { key: ConfigSection; label: string; icon: React.ReactNode; description: string }[] = [
    { key: "nginx", label: "Nginx", icon: <Globe size={18} className="text-green-400" />, description: "Web server configuration" },
    { key: "php", label: "PHP", icon: <Code size={18} className="text-purple-400" />, description: "PHP-FPM configuration" },
    { key: "mongodb", label: "MongoDB", icon: <Database size={18} className="text-green-400" />, description: "Database server configuration" },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Configuration</h1>
          <p className="text-panel-muted text-sm mt-1">
            View and edit server configuration files
          </p>
        </div>
        <Button
          onClick={fetchConfig}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      {/* Section Cards */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        {sections.map((section) => (
          <Card key={section.key}>
            <button
              onClick={() => {
                setActiveSection(section.key);
                setEditing(false);
              }}
              className={`w-full p-5 text-left rounded-lg transition-colors ${
                activeSection === section.key
                  ? "ring-2 ring-blue-500 bg-blue-500/5"
                  : "hover:bg-panel-border/10"
              }`}
            >
              <div className="flex items-center gap-3 mb-2">
                <div className="p-2 rounded-lg bg-panel-bg">{section.icon}</div>
                <div>
                  <h3 className="font-semibold text-panel-text">{section.label}</h3>
                  <p className="text-xs text-panel-muted">{section.description}</p>
                </div>
              </div>
            </button>
          </Card>
        ))}
      </div>

      {/* Config Content */}
      <Card>
        <div className="p-5 border-b border-panel-border flex items-center justify-between">
          <div className="flex items-center gap-2">
            <FileText size={16} className="text-panel-muted" />
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
              {sections.find((s) => s.key === activeSection)?.label} Configuration
            </h3>
          </div>
          <div className="flex items-center gap-2">
            {editing ? (
              <>
                <Button
                  onClick={() => setEditing(false)}
                  className="flex items-center gap-1 px-3 py-1.5 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-xs font-medium transition-colors"
                >
                  Cancel
                </Button>
                <Button
                  onClick={handleSave}
                  className="flex items-center gap-1 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-xs font-medium transition-colors"
                >
                  <Save size={12} />
                  Save Changes
                </Button>
              </>
            ) : (
              <Button
                onClick={handleEdit}
                className="flex items-center gap-1 px-3 py-1.5 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-xs font-medium transition-colors"
              >
                <Edit size={12} />
                Edit
              </Button>
            )}
          </div>
        </div>
        <div className="p-5">
          {loading ? (
            <div className="h-96 bg-panel-bg rounded-lg animate-pulse" />
          ) : editing ? (
            <textarea
              value={editContent}
              onChange={(e) => setEditContent(e.target.value)}
              className="w-full h-96 p-4 bg-panel-bg border border-panel-border rounded-lg text-panel-text font-mono text-sm focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 resize-y"
              spellCheck={false}
            />
          ) : (
            configs[activeSection] ? (
              <CodeBlock
                code={configs[activeSection]}
                language={activeSection === "mongodb" ? "yaml" : activeSection === "php" ? "ini" : "nginx"}
              />
            ) : (
              <div className="text-center py-16 px-4">
                <FileText size={48} className="text-panel-muted/20 mx-auto mb-4" />
                <h3 className="text-lg font-medium text-panel-text mb-1">No configuration loaded</h3>
                <p className="text-panel-muted text-sm">
                  Could not load the {activeSection} configuration. Ensure the server agent is running.
                </p>
              </div>
            )
          )}
        </div>
      </Card>

      {/* Warning */}
      <Card>
        <div className="p-4 flex items-start gap-3">
          <Settings size={18} className="text-yellow-400 shrink-0 mt-0.5" />
          <div>
            <p className="text-sm font-medium text-panel-text">Configuration Changes</p>
            <p className="text-xs text-panel-muted mt-0.5">
              Changes to server configuration files require a service restart to take effect. Incorrect configuration may cause service outages.
            </p>
          </div>
        </div>
      </Card>
    </div>
  );
}
