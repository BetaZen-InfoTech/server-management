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
      // Use placeholder configs
      setConfigs((prev) => ({
        ...prev,
        [activeSection]: getPlaceholderConfig(activeSection),
      }));
    } finally {
      setLoading(false);
    }
  };

  const getPlaceholderConfig = (section: ConfigSection): string => {
    switch (section) {
      case "nginx":
        return `# /etc/nginx/nginx.conf
user www-data;
worker_processes auto;
pid /run/nginx.pid;

events {
    worker_connections 1024;
    multi_accept on;
}

http {
    sendfile on;
    tcp_nopush on;
    tcp_nodelay on;
    keepalive_timeout 65;
    types_hash_max_size 2048;
    server_tokens off;

    include /etc/nginx/mime.types;
    default_type application/octet-stream;

    # SSL Settings
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers on;

    # Logging
    access_log /var/log/nginx/access.log;
    error_log /var/log/nginx/error.log;

    # Gzip
    gzip on;
    gzip_vary on;
    gzip_proxied any;
    gzip_comp_level 6;
    gzip_types text/plain text/css application/json application/javascript text/xml;

    include /etc/nginx/conf.d/*.conf;
    include /etc/nginx/sites-enabled/*;
}`;
      case "php":
        return `; /etc/php/8.3/fpm/php.ini

[PHP]
engine = On
short_open_tag = Off
precision = 14
output_buffering = 4096
zlib.output_compression = Off

max_execution_time = 300
max_input_time = 300
memory_limit = 256M

error_reporting = E_ALL & ~E_DEPRECATED & ~E_STRICT
display_errors = Off
log_errors = On
error_log = /var/log/php/error.log

post_max_size = 64M
upload_max_filesize = 64M
max_file_uploads = 20

date.timezone = UTC

[opcache]
opcache.enable=1
opcache.memory_consumption=128
opcache.interned_strings_buffer=8
opcache.max_accelerated_files=10000
opcache.revalidate_freq=60

[Session]
session.save_handler = files
session.save_path = "/var/lib/php/sessions"
session.gc_maxlifetime = 1440`;
      case "mongodb":
        return `# /etc/mongod.conf

storage:
  dbPath: /var/lib/mongodb
  journal:
    enabled: true
  engine: wiredTiger
  wiredTiger:
    engineConfig:
      cacheSizeGB: 1

systemLog:
  destination: file
  logAppend: true
  path: /var/log/mongodb/mongod.log

net:
  port: 27017
  bindIp: 127.0.0.1

processManagement:
  timeZoneInfo: /usr/share/zoneinfo

security:
  authorization: enabled

operationProfiling:
  mode: slowOp
  slowOpThresholdMs: 100`;
      default:
        return "# Configuration not available";
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
            <CodeBlock
              code={configs[activeSection] || getPlaceholderConfig(activeSection)}
              language={activeSection === "mongodb" ? "yaml" : activeSection === "php" ? "ini" : "nginx"}
            />
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
