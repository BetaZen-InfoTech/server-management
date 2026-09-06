package services

import "fmt"

// Preset describes a framework preset that can auto-fill install/build/start
// commands and optionally scaffold a minimal runnable demo app on the server.
//
// JSON tags are populated so the frontend can fetch this list from
// GET /api/v1/whm/apps/presets instead of mirroring it in TypeScript.
//
// The three commands are run in this order on deploy:
//  1. InstallCmd — fetch dependencies (npm install, pip install, go mod
//     download, bundle install, …). Idempotent, safe to re-run on Redeploy.
//  2. BuildCmd — compile / bundle / transpile (npm run build, go build,
//     tsc, webpack, …). May be empty for interpreted-only stacks.
//  3. StartCmd — what systemd ExecStarts when the service comes up.
//
// Splitting install from build lets the UI show three distinct commands so
// operators know exactly what will run, and it lets the backend skip an
// empty build step cleanly instead of having it fall out of a chained
// `npm install && npm run build` string.
type Preset struct {
	Framework   string `json:"framework"`
	Label       string `json:"label"`
	AppType     string `json:"app_type"`
	DefaultPort int    `json:"default_port"`
	InstallCmd  string `json:"install_cmd"`
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
	// -------------------------- Node.js --------------------------
	"node-express": {
		Framework:   "node-express",
		Label:       "Node.js (Express / vanilla)",
		AppType:     "node",
		DefaultPort: 3000,
		InstallCmd:  "npm install --omit=dev --no-audit --no-fund --loglevel=error",
		BuildCmd:    "",
		// Bare `node` so PATH picks it up — lets Advanced Options →
		// Runtime version swap in /usr/local/n/versions/node/<v>/bin
		// without having to rewrite the start command.
		StartCmd: "node server.js",
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

	"nuxt": {
		Framework:   "nuxt",
		Label:       "Nuxt 3 (Vue SSR)",
		AppType:     "node",
		DefaultPort: 3000,
		InstallCmd:  "npm install --no-audit --no-fund --loglevel=error",
		BuildCmd:    "npm run build",
		// Nuxt 3's build emits a self-contained Node server at
		// .output/server/index.mjs. It reads $PORT and $HOST from the
		// environment (NITRO_PORT also works) — no extra CLI flags
		// needed, which keeps the systemd unit clean.
		StartCmd: "node .output/server/index.mjs",
		Scaffold: map[string]string{
			"package.json": `{
  "name": "sp-demo-nuxt",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "dev": "nuxt dev",
    "build": "nuxt build",
    "start": "node .output/server/index.mjs"
  },
  "dependencies": {
    "nuxt": "3.13.0",
    "vue": "3.4.38",
    "vue-router": "4.4.3"
  }
}
`,
			"nuxt.config.ts": `export default defineNuxtConfig({
  devtools: { enabled: false },
  nitro: {
    // Bind to all interfaces so the systemd unit's $PORT lands the
    // server on a socket nginx can reach from the host network.
    preset: 'node-server',
  },
});
`,
			"app.vue": `<template>
  <main style="font-family: system-ui; padding: 24px;">
    <h1>SP Demo Nuxt 3</h1>
    <p>Deployed by Betazen Server Panel at {{ now }}</p>
    <p>Framework: Nuxt 3 (Vue SSR)</p>
  </main>
</template>

<script setup>
const now = new Date().toISOString();
</script>
`,
		},
	},

	"nextjs": {
		Framework:   "nextjs",
		Label:       "Next.js 14 (App Router)",
		AppType:     "node",
		DefaultPort: 3000,
		InstallCmd:  "npm install --no-audit --no-fund --loglevel=error",
		BuildCmd:    "npm run build",
		StartCmd:    "npx next start -p ${PORT}",
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
    <p>Deployed by Betazen Server Panel at {new Date().toISOString()}</p>
    <p>Framework: Next.js 14 (App Router)</p>
  </main>);
}
`,
		},
	},

	"nestjs": {
		Framework:   "nestjs",
		Label:       "NestJS (Node)",
		AppType:     "node",
		DefaultPort: 3000,
		// NO --omit=dev: `nest build` needs @nestjs/cli + typescript, which
		// live in devDependencies. Mirrors the nextjs/nuxt presets.
		InstallCmd: "npm install --no-audit --no-fund --loglevel=error",
		// `nest build` transpiles src/ → dist/ (main.ts → dist/main.js).
		BuildCmd: "npm run build",
		// Bare `node` (not `npm run start:prod`) so Advanced Options →
		// Runtime version can swap /usr/local/n/versions/node/<v>/bin onto
		// PATH without rewriting the start command — same rationale as the
		// node-express preset. `nest build` with sourceRoot=src + the default
		// outDir=./dist emits dist/main.js.
		StartCmd: "node dist/main.js",
		Scaffold: map[string]string{
			"package.json": `{
  "name": "sp-demo-nestjs",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "build": "nest build",
    "start": "nest start",
    "start:prod": "node dist/main"
  },
  "dependencies": {
    "@nestjs/common": "10.4.4",
    "@nestjs/core": "10.4.4",
    "@nestjs/platform-express": "10.4.4",
    "reflect-metadata": "0.2.2",
    "rxjs": "7.8.1"
  },
  "devDependencies": {
    "@nestjs/cli": "10.4.5",
    "@nestjs/schematics": "10.1.4",
    "@types/node": "20.14.0",
    "typescript": "5.5.4"
  }
}
`,
			"nest-cli.json": `{
  "$schema": "https://json.schemastore.org/nest-cli",
  "collection": "@nestjs/schematics",
  "sourceRoot": "src"
}
`,
			"tsconfig.json": `{
  "compilerOptions": {
    "module": "commonjs",
    "declaration": false,
    "emitDecoratorMetadata": true,
    "experimentalDecorators": true,
    "target": "ES2021",
    "sourceMap": false,
    "outDir": "./dist",
    "baseUrl": "./",
    "esModuleInterop": true,
    "skipLibCheck": true,
    "strictNullChecks": false,
    "forceConsistentCasingInFileNames": true
  }
}
`,
			"tsconfig.build.json": `{
  "extends": "./tsconfig.json",
  "exclude": ["node_modules", "dist", "test", "**/*spec.ts"]
}
`,
			"src/main.ts": `import { NestFactory } from '@nestjs/core';
import { AppModule } from './app.module';

async function bootstrap() {
  const app = await NestFactory.create(AppModule);
  // Bind 0.0.0.0 so nginx can reach the service on the panel-assigned $PORT.
  const port = parseInt(process.env.PORT || '3000', 10);
  await app.listen(port, '0.0.0.0');
  console.log('sp-demo-nestjs listening on ' + port);
}
bootstrap();
`,
			"src/app.module.ts": `import { Module } from '@nestjs/common';
import { AppController } from './app.controller';
import { AppService } from './app.service';

@Module({
  controllers: [AppController],
  providers: [AppService],
})
export class AppModule {}
`,
			"src/app.controller.ts": `import { Controller, Get } from '@nestjs/common';
import { AppService } from './app.service';

@Controller()
export class AppController {
  constructor(private readonly appService: AppService) {}

  @Get()
  getInfo() {
    return this.appService.info();
  }
}
`,
			"src/app.service.ts": `import { Injectable } from '@nestjs/common';

@Injectable()
export class AppService {
  info() {
    return {
      app: 'sp-demo-nestjs',
      framework: 'NestJS (Node)',
      node: process.version,
      ts: new Date().toISOString(),
    };
  }
}
`,
		},
	},

	"react-vite": {
		Framework:   "react-vite",
		Label:       "React + Vite (static)",
		AppType:     "static",
		DefaultPort: 0,
		InstallCmd:  "npm install --no-audit --no-fund --loglevel=error",
		BuildCmd:    "npm run build",
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
  <p>Static build deployed by Betazen Server Panel.</p>
</div>); }
createRoot(document.getElementById('root')).render(<App/>);
`,
		},
	},

	"vue-vite": {
		Framework:   "vue-vite",
		Label:       "Vue 3 + Vite (static)",
		AppType:     "static",
		DefaultPort: 0,
		InstallCmd:  "npm install --no-audit --no-fund --loglevel=error",
		BuildCmd:    "npm run build",
		IsStatic:    true,
		StaticDir:   "dist",
		// Mirror of the react-vite preset: a single-page app that builds
		// to dist/ and is served by nginx with try_files SPA fallback
		// (the static-vhost generator handles that). No systemd unit.
		Scaffold: map[string]string{
			"package.json": `{
  "name": "sp-demo-vue-vite",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "vue": "3.4.38"
  },
  "devDependencies": {
    "@vitejs/plugin-vue": "5.1.2",
    "vite": "5.4.2"
  }
}
`,
			"vite.config.js": `import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
export default defineConfig({ plugins: [vue()] });
`,
			"index.html": `<!doctype html>
<html><head><meta charset="utf-8"><title>SP Demo Vue (Vite)</title></head>
<body><div id="app"></div><script type="module" src="/src/main.js"></script></body></html>
`,
			"src/main.js": `import { createApp } from 'vue';
import App from './App.vue';
createApp(App).mount('#app');
`,
			"src/App.vue": `<template>
  <div style="font-family: system-ui; padding: 24px;">
    <h1>SP Demo Vue (Vite)</h1>
    <p>Static build deployed by Betazen Server Panel.</p>
    <p>Vue version: {{ vueVersion }}</p>
  </div>
</template>

<script setup>
import { version as vueVersion } from 'vue';
</script>
`,
		},
	},

	"vue-express": {
		Framework:   "vue-express",
		Label:       "Vue 3 + Express (fullstack)",
		AppType:     "node",
		DefaultPort: 3000,
		InstallCmd:  "npm install --no-audit --no-fund --loglevel=error",
		BuildCmd:    "npm run build",
		// Express on $PORT serves /api/*. Pick service role=fullstack
		// when adding the service so the vhost gets BOTH the static
		// root (dist/) AND the /api proxy to this backend wired.
		// (buildRecoveryVhostSpec handles the fullstack pattern at
		// transfer_panel_records.go; see project_helpers_test.go for
		// the canonical shape.)
		StartCmd: "node server.js",
		Scaffold: map[string]string{
			"package.json": `{
  "name": "sp-demo-vue-express",
  "version": "1.0.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "start": "node server.js"
  },
  "dependencies": {
    "express": "4.19.2",
    "vue": "3.4.38"
  },
  "devDependencies": {
    "@vitejs/plugin-vue": "5.1.2",
    "vite": "5.4.2"
  }
}
`,
			"vite.config.js": `import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';

// During local dev (vite dev) proxy /api to the Express server so
// fetch('/api/...') in App.vue Just Works without CORS headers.
// In production nginx does the same proxy — see the panel's
// fullstack vhost template.
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:3000',
    },
  },
});
`,
			"index.html": `<!doctype html>
<html><head><meta charset="utf-8"><title>SP Demo Vue + Express</title></head>
<body><div id="app"></div><script type="module" src="/src/main.js"></script></body></html>
`,
			"src/main.js": `import { createApp } from 'vue';
import App from './App.vue';
createApp(App).mount('#app');
`,
			"src/App.vue": `<template>
  <main style="font-family: system-ui; padding: 24px;">
    <h1>SP Demo Vue + Express (fullstack)</h1>
    <p>Vue {{ vueVersion }} frontend served by nginx from dist/</p>
    <p>Express backend reply: <code>{{ apiReply }}</code></p>
    <button @click="ping">Ping /api/hello</button>
  </main>
</template>

<script setup>
import { ref, onMounted } from 'vue';
import { version as vueVersion } from 'vue';

const apiReply = ref('(loading…)');

async function ping() {
  try {
    const r = await fetch('/api/hello');
    apiReply.value = JSON.stringify(await r.json());
  } catch (e) {
    apiReply.value = 'ERROR: ' + e.message;
  }
}

onMounted(ping);
</script>
`,
			"server.js": `// Express backend for the fullstack Vue preset.
//
// Picks $PORT from env (the panel's systemd unit injects PORT=<assigned>);
// falls back to 3000 for local dev.
//
// Routes:
//   GET /api/hello — small JSON ping the scaffold UI calls onMounted.
//
// nginx serves the Vue dist/ for everything else — see the panel's
// fullstack vhost template (Root=dist/, Proxies=[{Prefix:/api, Port}]).

import express from 'express';

const app = express();
const port = parseInt(process.env.PORT || '3000', 10);

app.get('/api/hello', (req, res) => {
  res.json({
    app: 'sp-demo-vue-express',
    framework: 'Vue 3 + Express (fullstack)',
    node: process.version,
    ts: new Date().toISOString(),
  });
});

app.listen(port, '0.0.0.0', () => {
  console.log('sp-demo-vue-express backend on ' + port);
});
`,
		},
	},

	// -------------------------- Python --------------------------
	"python-flask": {
		Framework:   "python-flask",
		Label:       "Python (Flask + gunicorn)",
		AppType:     "python",
		DefaultPort: 5000,
		InstallCmd:  "python3 -m venv venv && ./venv/bin/pip install --quiet --disable-pip-version-check flask gunicorn",
		BuildCmd:    "",
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

	// -------------------------- Ruby --------------------------
	"ruby-sinatra": {
		Framework:   "ruby-sinatra",
		Label:       "Ruby (Sinatra)",
		AppType:     "ruby",
		DefaultPort: 4567,
		InstallCmd:  "bundle config set --local path 'vendor/bundle' && bundle install --quiet",
		BuildCmd:    "",
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

	// -------------------------- Go --------------------------
	// All Go presets share the same install/build/start shape:
	//   install: go mod download   — pulls modules declared in go.mod
	//   build:   go build -o app . — compiles a static binary named "app"
	//   start:   ./app             — the binary reads PORT from env
	// The systemd WorkingDirectory is set to the app dir, so ./app resolves
	// correctly. Go apps do NOT use PM2 — the compiled binary is its own
	// process supervisor's workload and systemd Restart=always covers
	// crash recovery.
	"go-vanilla": {
		Framework:   "go-vanilla",
		Label:       "Go (net/http, stdlib)",
		AppType:     "go",
		DefaultPort: 8080,
		InstallCmd:  "go mod tidy",
		BuildCmd:    "go build -o app .",
		StartCmd:    "./app",
		Scaffold: map[string]string{
			"go.mod": `module sp-demo-go-vanilla

go 1.22
`,
			"main.go": `package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"app":       "sp-demo-go-vanilla",
			"framework": "Go net/http",
			"go":        runtime.Version(),
			"path":      r.URL.Path,
			"ts":        time.Now().UTC().Format(time.RFC3339),
		})
	})
	log.Printf("sp-demo-go-vanilla listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
`,
		},
	},

	"go-gin": {
		Framework:   "go-gin",
		Label:       "Go (Gin)",
		AppType:     "go",
		DefaultPort: 8080,
		InstallCmd:  "go mod tidy",
		BuildCmd:    "go build -o app .",
		StartCmd:    "./app",
		Scaffold: map[string]string{
			"go.mod": `module sp-demo-go-gin

go 1.22

require github.com/gin-gonic/gin v1.10.0
`,
			"main.go": `package main

import (
	"os"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	r := gin.Default()
	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"app":       "sp-demo-go-gin",
			"framework": "Gin",
			"go":        runtime.Version(),
			"ts":        time.Now().UTC().Format(time.RFC3339),
		})
	})
	_ = r.Run(":" + port)
}
`,
		},
	},

	"go-fiber": {
		Framework:   "go-fiber",
		Label:       "Go (Fiber)",
		AppType:     "go",
		DefaultPort: 8080,
		InstallCmd:  "go mod tidy",
		BuildCmd:    "go build -o app .",
		StartCmd:    "./app",
		Scaffold: map[string]string{
			"go.mod": `module sp-demo-go-fiber

go 1.22

require github.com/gofiber/fiber/v2 v2.52.5
`,
			"main.go": `package main

import (
	"log"
	"os"
	"runtime"
	"time"

	"github.com/gofiber/fiber/v2"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"app":       "sp-demo-go-fiber",
			"framework": "Fiber",
			"go":        runtime.Version(),
			"ts":        time.Now().UTC().Format(time.RFC3339),
		})
	})
	log.Fatal(app.Listen(":" + port))
}
`,
		},
	},

	"go-echo": {
		Framework:   "go-echo",
		Label:       "Go (Echo)",
		AppType:     "go",
		DefaultPort: 8080,
		InstallCmd:  "go mod tidy",
		BuildCmd:    "go build -o app .",
		StartCmd:    "./app",
		Scaffold: map[string]string{
			"go.mod": `module sp-demo-go-echo

go 1.22

require github.com/labstack/echo/v4 v4.12.0
`,
			"main.go": `package main

import (
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/labstack/echo/v4"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	e := echo.New()
	e.GET("/", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"app":       "sp-demo-go-echo",
			"framework": "Echo",
			"go":        runtime.Version(),
			"ts":        time.Now().UTC().Format(time.RFC3339),
		})
	})
	e.Logger.Fatal(e.Start(":" + port))
}
`,
		},
	},

	"go-chi": {
		Framework:   "go-chi",
		Label:       "Go (Chi router)",
		AppType:     "go",
		DefaultPort: 8080,
		InstallCmd:  "go mod tidy",
		BuildCmd:    "go build -o app .",
		StartCmd:    "./app",
		Scaffold: map[string]string{
			"go.mod": `module sp-demo-go-chi

go 1.22

require github.com/go-chi/chi/v5 v5.1.0
`,
			"main.go": `package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/go-chi/chi/v5"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"app":       "sp-demo-go-chi",
			"framework": "Chi",
			"go":        runtime.Version(),
			"ts":        time.Now().UTC().Format(time.RFC3339),
		})
	})
	log.Fatal(http.ListenAndServe(":"+port, r))
}
`,
		},
	},
}

// lookupPreset returns a preset by framework name (case-insensitive).
func lookupPreset(framework string) (Preset, bool) {
	p, ok := presets[framework]
	return p, ok
}

// missingBuildCmdHint returns an example install command for a given app type
// if that type requires one. Used by the Deploy error message so operators
// see a copy-pasteable hint, not a generic "install command required".
// Returns ok=false for types that don't need an install step (docker images,
// pre-built binaries, static sites, PHP, Java wars).
//
// Name is historical — it covers install-or-build, whichever the app type
// needs to run its first deploy. The UI splits the two in the form.
func missingBuildCmdHint(appType string) (string, bool) {
	switch appType {
	case "node", "nodejs":
		return "npm install --omit=dev", true
	case "python":
		return "python3 -m venv venv && ./venv/bin/pip install -r requirements.txt", true
	case "ruby":
		return "bundle config set --local path 'vendor/bundle' && bundle install", true
	case "go":
		// `go mod tidy` resolves imports + downloads + writes go.sum
		// from go.mod alone — works on a fresh scaffold (where no
		// go.sum exists yet) AND on an imported project that already
		// has one. `go mod download` would only work in the second
		// case and fails with "missing go.sum entry" on the first,
		// which is exactly the build error operators kept hitting on
		// the very first deploy of a scaffolded project.
		return "go mod tidy && go build -o app .", true
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
