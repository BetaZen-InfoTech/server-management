package models

type NginxConfig struct {
	WorkerProcesses    string   `json:"worker_processes"`
	WorkerConnections  int      `json:"worker_connections"`
	KeepaliveTimeout   int      `json:"keepalive_timeout"`
	ClientMaxBodySize  string   `json:"client_max_body_size"`
	Gzip               bool     `json:"gzip"`
	GzipTypes          []string `json:"gzip_types"`
	ServerTokens       bool     `json:"server_tokens"`
	RateLimitEnabled   bool     `json:"rate_limit_enabled"`
	RateLimitRequests  int      `json:"rate_limit_requests"`
	RateLimitBurst     int      `json:"rate_limit_burst"`
}

type PHPConfig struct {
	MemoryLimit       string `json:"memory_limit"`
	MaxExecutionTime  int    `json:"max_execution_time"`
	MaxInputTime      int    `json:"max_input_time"`
	PostMaxSize       string `json:"post_max_size"`
	UploadMaxFilesize string `json:"upload_max_filesize"`
	MaxFileUploads    int    `json:"max_file_uploads"`
	DisplayErrors     bool   `json:"display_errors"`
	ErrorReporting    string `json:"error_reporting"`
	DateTimezone      string `json:"date_timezone"`
	OpcacheEnabled    bool   `json:"opcache_enabled"`
	OpcacheMemory     int    `json:"opcache_memory"`
}

type MongoDBConfig struct {
	StorageEngine        string `json:"storage_engine"`
	CacheSizeGB          float64 `json:"cache_size_gb"`
	MaxConnections       int    `json:"max_connections"`
	JournalEnabled       bool   `json:"journal_enabled"`
	SlowQueryThresholdMS int    `json:"slow_query_threshold_ms"`
	ProfilingLevel       int    `json:"profiling_level"`
	BindIP               string `json:"bind_ip"`
	AuthEnabled          bool   `json:"auth_enabled"`
}

type MaintenanceConfig struct {
	Enabled        bool     `json:"enabled"`
	Message        string   `json:"message"`
	AllowedIPs     []string `json:"allowed_ips"`
	EstimatedEnd   string   `json:"estimated_end"`
	CustomPageHTML string   `json:"custom_page_html"`
	RetryAfter     int      `json:"retry_after"`
}
