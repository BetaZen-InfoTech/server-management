package services

import "fmt"

// Preset describes a framework preset that can auto-fill build/start commands
// and optionally scaffold a minimal runnable demo app on the server.
//
// JSON tags are populated so the frontend can fetch this list from
// GET /api/v1/whm/apps/presets instead of mirroring it in TypeScript.
type Preset struct {
	Framework   string `json:"framework"`
	Label       string `json:"label"`
	AppType     string `json:"app_type"`
	DefaultPort int    `json:"default_port"`
	BuildCmd    string `json:"build_cmd"`
	StartCmd    string `json:"start_cmd"` // may contain ${PORT} placeholder
	IsStatic    bool   `json:"is_static"` // served by nginx directly, no systemd service
	StaticDir   string `json:"static_dir,omitempty"`
	// Scaffold is excluded from the public JSON — it's a server-side detail
	// and blowing it onto every frontend render would be a lot of bytes.
	Scaffold map[string]string `json:"-"`
}

// presets contains known framework presets keyed by framework identifier.
// Framework identifiers mirror what the frontend sends.
var presets = map[string]Preset{
	"node-express": {
		Framework:   "node-express",
		Label:       "Node.js (Express / vanilla)",
		AppType:     "node",
		DefaultPort: 3000,
		BuildCmd:    "npm install --omit=dev --no-audit --no-fund --loglevel=error",
		StartCmd:    "/usr/local/bin/node server.js",
		Scaffold: map[string]string{
			"package.json": `{
  "name": "sp-demo-node-express",
  "version": "1.0.0",
  "private": true,
  "main": "server.js",
  "scripts": { "start": "node server.js" }
}
`,
			"server.js": `const http = require('http');
const port = parseInt(process.env.PORT || '3000', 10);
const server = http.createServer((req, res) => {
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({
    app: 'sp-demo-node-express',
    framework: 'Node.js (built-in http)',
    node: process.version,
    path: req.url,
    ts: new Date().toISOString(),
  }));
});
server.listen(port, '0.0.0.0', () => {
  console.log('sp-demo-node-express listening on ' + port);
});
`,
		},
	},

	"nextjs": {
		Framework:   "nextjs",
		Label:       "Next.js 14 (App Router)",
		AppType:     "node",
		DefaultPort: 3000,
		BuildCmd:    "npm install --no-audit --no-fund --loglevel=error && npm run build",
		StartCmd:    "/usr/local/bin/npx next start -p ${PORT}",
		Scaffold: map[string]string{
			"package.json": `{
  "name": "sp-demo-nextjs",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "dev": "next dev",
    "build": "next build",
    "start": "next start"
  },
  "dependencies": {
    "next": "14.2.5",
    "react": "18.3.1",
    "react-dom": "18.3.1"
  }
}
`,
			"next.config.js": `module.exports = { reactStrictMode: true, output: 'standalone' };
`,
			"app/layout.js": `export const metadata = { title: 'SP Demo Next.js' };
export default function RootLayout({ children }) {
  return (<html lang="en"><body style={{fontFamily:'system-ui',padding:24}}>{children}</body></html>);
}
`,
			"app/page.js": `export default function Home() {
  return (<main>
    <h1>SP Demo Next.js</h1>
    <p>Deployed by ServerPanel at {new Date().toISOString()}</p>
    <p>Framework: Next.js 14 (App Router)</p>
  </main>);
}
`,
		},
	},

	"react-vite": {
		Framework:   "react-vite",
		Label:       "React + Vite (static)",
		AppType:     "static",
		DefaultPort: 0,
		BuildCmd:    "npm install --no-audit --no-fund --loglevel=error && npm run build",
		IsStatic:    true,
		StaticDir:   "dist",
		Scaffold: map[string]string{
			"package.json": `{
  "name": "sp-demo-react-vite",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "dev": "vite",
    "build": "vite build"
  },
  "dependencies": {
    "react": "18.3.1",
    "react-dom": "18.3.1"
  },
  "devDependencies": {
    "@vitejs/plugin-react": "4.3.1",
    "vite": "5.4.2"
  }
}
`,
			"vite.config.js": `import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
export default defineConfig({ plugins: [react()] });
`,
			"index.html": `<!doctype html>
<html><head><meta charset="utf-8"><title>SP Demo React (Vite)</title></head>
<body><div id="root"></div><script type="module" src="/src/main.jsx"></script></body></html>
`,
			"src/main.jsx": `import React from 'react';
import { createRoot } from 'react-dom/client';
function App(){ return (<div style={{fontFamily:'system-ui',padding:24}}>
  <h1>SP Demo React (Vite)</h1>
  <p>Static build deployed by ServerPanel.</p>
</div>); }
createRoot(document.getElementById('root')).render(<App/>);
`,
		},
	},

	"python-flask": {
		Framework:   "python-flask",
		Label:       "Python (Flask + gunicorn)",
		AppType:     "python",
		DefaultPort: 5000,
		BuildCmd:    "python3 -m venv venv && ./venv/bin/pip install --quiet --disable-pip-version-check flask gunicorn",
		StartCmd:    "./venv/bin/gunicorn --bind 0.0.0.0:${PORT} --workers 2 app:app",
		Scaffold: map[string]string{
			"app.py": `from flask import Flask, jsonify
import datetime, sys
app = Flask(__name__)

@app.route('/')
def index():
    return jsonify({
        'app': 'sp-demo-python-flask',
        'framework': 'Flask + gunicorn',
        'python': sys.version.split()[0],
        'ts': datetime.datetime.utcnow().isoformat() + 'Z',
    })

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=5000)
`,
			"requirements.txt": "flask\ngunicorn\n",
		},
	},

	"ruby-sinatra": {
		Framework:   "ruby-sinatra",
		Label:       "Ruby (Sinatra)",
		AppType:     "ruby",
		DefaultPort: 4567,
		BuildCmd:    "bundle config set --local path 'vendor/bundle' && bundle install --quiet",
		StartCmd:    "bundle exec ruby app.rb -o 0.0.0.0 -p ${PORT}",
		Scaffold: map[string]string{
			"Gemfile": `source 'https://rubygems.org'
gem 'sinatra'
gem 'puma'
gem 'rackup'
`,
			"app.rb": `require 'sinatra'
require 'json'
set :bind, '0.0.0.0'
# Sinatra 4.x enables Rack::Protection::HostAuthorization by default, which
# rejects requests whose Host header doesn't match an allowed pattern. Since
# this app sits behind nginx (which forwards the original Host) and the
# allowed domain isn't known at scaffold time, disable the check.
set :host_authorization, { permitted_hosts: [] }
get '/' do
  content_type :json
  {
    app: 'sp-demo-ruby-sinatra',
    framework: 'Sinatra (Ruby)',
    ruby: RUBY_VERSION,
    ts: Time.now.utc.iso8601,
  }.to_json
end
`,
		},
	},
}

// lookupPreset returns a preset by framework name (case-insensitive).
func lookupPreset(framework string) (Preset, bool) {
	p, ok := presets[framework]
	return p, ok
}

// missingBuildCmdHint returns an example build command for a given app type
// if that type requires one. Used by the Deploy error message so operators
// see a copy-pasteable hint, not a generic "build command required".
// Returns ok=false for types that don't need a build step (docker images,
// pre-built binaries, static sites, PHP, Java wars).
func missingBuildCmdHint(appType string) (string, bool) {
	switch appType {
	case "node", "nodejs":
		return "npm install --omit=dev", true
	case "python":
		return "python3 -m venv venv && ./venv/bin/pip install -r requirements.txt", true
	case "ruby":
		return "bundle config set --local path 'vendor/bundle' && bundle install", true
	case "go":
		return "go build -o app ./...", true
	case "rust":
		return "cargo build --release", true
	}
	return "", false
}

// GetPresets returns all known framework presets, keyed by framework id.
// Exported so handlers can serve GET /api/v1/whm/apps/presets — the
// frontend fetches this list instead of hard-coding a parallel copy,
// which used to drift out of sync with the server-side defaults.
func GetPresets() map[string]Preset {
	out := make(map[string]Preset, len(presets))
	for k, v := range presets {
		out[k] = v
	}
	return out
}

// renderStartCmd substitutes ${PORT} placeholders in a start command with the
// actual port value.
func renderStartCmd(cmd string, port int) string {
	out := ""
	placeholder := "${PORT}"
	for {
		i := indexOf(cmd, placeholder)
		if i < 0 {
			out += cmd
			break
		}
		out += cmd[:i] + fmt.Sprintf("%d", port)
		cmd = cmd[i+len(placeholder):]
	}
	return out
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 || m > n {
		return -1
	}
	for i := 0; i <= n-m; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
