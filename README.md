# BrowseGemini 🌐🤖

[![Go Report Card](https://goreportcard.com/badge/github.com/Maher-W/BrowseGemini)](https://goreportcard.com/report/github.com/Maher-W/BrowseGemini)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?logo=docker&logoColor=white)](server/Dockerfile)

**BrowseGemini** is an open-source, ultra-lightweight system (~10MB RAM) that connects **Google Gemini** (via [gemini.google.com](https://gemini.google.com) Custom Connected Apps) directly to your live **Google Chrome** browser using a remote Model Context Protocol (MCP) server and a Manifest V3 Chrome Extension.

Gemini can view tabs, take screenshots, inspect interactive elements, click, drag, scroll, type, and execute keyboard shortcuts on your live browser.

---

## ⚡ Architecture & How It Works

```
┌──────────────────┐      HTTPS Streamable MCP + OAuth 2.0      ┌───────────────────────┐
│  Google Gemini   │ ◄────────────────────────────────────────► │   BrowseGemini Server │
│(gemini.google.com)│                                            │  (Go, Port 8080)      │
└──────────────────┘                                            └───────────────────────┘
                                                                            ▲
                                                                            │ WSS WebSocket
                                                                            │ + Password Auth
                                                                            ▼
┌──────────────────┐          Chrome DevTools Protocol (CDP)    ┌───────────────────────┐
│ Live Chrome Tabs │ ◄────────────────────────────────────────► │   Chrome Extension    │
│  (Your Browser)  │                                            │     (Manifest V3)     │
└──────────────────┘                                            └───────────────────────┘
```

- **Ultra-lightweight:** Minimal Go binary inside Alpine Linux (~10MB RAM).
- **Single password:** One `ADMIN_PASSWORD` is used for both Gemini OAuth login and Chrome Extension WebSocket authentication.
- **Single-port multiplexing:** HTTP MCP transport, OAuth 2.0 discovery, and WebSockets all run on port `8080` (ready for Coolify, Docker, or reverse proxies with zero config).
- **Privacy-first:** Master ON/OFF toggle in the extension popup immediately severs the WebSocket and detaches the debugger from all tabs.

---

## 🚀 Quick Start (Running on Your Laptop)

### 1. Run the Server

**Using Go:**
```bash
cd server
cp ../.env.example .env
go run ./cmd/server
```

**Using Docker:**
```bash
docker build -t browse-gemini .
docker run -d -p 8080:8080 \
  -e ADMIN_PASSWORD="your-admin-password" \
  -v browse-gemini-data:/data \
  browse-gemini
```

The server will be running on `http://localhost:8080`.

---

### 2. Install the Chrome Extension

1. Open Google Chrome and navigate to `chrome://extensions/`.
2. Enable **Developer mode** (toggle in the top-right corner).
3. Click **Load unpacked** and select the [`extension/`](extension/) directory from this repository.
4. Click the **BrowseGemini** icon in your Chrome toolbar:
   - **Server Domain:** Enter your server domain (e.g. `browse.gemini.code.ebda3is.com` or `localhost:8080`)
   - **Server Password:** Enter the same `ADMIN_PASSWORD` you set in the server configuration.
   - **Allow Gemini on this device:** Check the box to authorize.
   - Click **Connect & Save**. The status badge will change to **ON — Connected**.

---

### 3. Connect Google Gemini

1. Go to [gemini.google.com](https://gemini.google.com).
2. Open **Settings** ⚙️ → **Connected apps** → **Add custom connected app**.
3. In the MCP Server URL input, enter:
   - For local running (via a tunnel like ngrok or cloudflared) or remote server:
     `https://mcp.yourdomain.com/mcp`
4. Gemini will discover OAuth metadata at `/.well-known/oauth-authorization-server` and open an authorization window.
5. Enter your `ADMIN_PASSWORD` to authorize the connection.
6. Done! Gemini is now connected to your live Chrome browser.

---

## ☁️ Deploying on a Remote Server (Coolify / Docker)

### Coolify Setup:
1. In Coolify, create a new resource → **Git Repository** pointing to `https://github.com/Maher-W/BrowseGemini`.
2. Set **Build Pack** to **Dockerfile** and base directory to `server` (or Dockerfile path `server/Dockerfile`).
3. Set **Exposed Port** to `8080`.
4. Add a Persistent Storage mount:
   - Destination: `/data`
   - Volume Name: `browse-gemini-data`
5. Configure Environment Variables:
   ```env
   PORT=8080
   HOST=0.0.0.0
   DATA_DIR=/data
   BASE_URL=https://mcp.yourdomain.com
   ADMIN_PASSWORD=choose-a-strong-password
   ```
6. Deploy! Coolify automatically sets up HTTPS and WebSocket (`wss://`) routing.

---

## 🛠️ Available MCP Tools

| Tool | Description |
| :--- | :--- |
| `browser_tabs` | List open tabs, open a new tab, switch tabs, or close a tab |
| `browser_screenshot` | Capture high-res viewport screenshot with coordinates matching viewport pixels |
| `browser_get_elements` | Returns visible clickable elements with center `(x, y)` coordinates and text labels |
| `browser_click` | Trusted click at `(x, y)` (left, right, middle, single or double click) |
| `browser_move_mouse` | Moves mouse cursor to coordinate `(x, y)` |
| `browser_drag` | Simulates smooth drag-and-drop gesture from `(startX, startY)` to `(endX, endY)` |
| `browser_scroll` | Dispatches smooth mouse wheel scrolling (horizontal and vertical) |
| `browser_type` | Types text into focused inputs with optional Enter key press |
| `browser_shortcut` | Hybrid shortcuts (browser-level `Ctrl+T`, `Ctrl+W`, `Alt+Left` + page-level `Ctrl+C`, `Ctrl+V`, `Ctrl+A`) |
| `browser_navigate` | Navigates to a URL or triggers `back`, `forward`, `reload` |

---

## 🔒 Security & Privacy

- **No Data Shared When OFF:** When toggled OFF in the extension popup, the WebSocket disconnects, all CDP sessions detach, Chrome's debugging banner disappears, and Gemini cannot access any tabs.
- **Single Password Auth:** One `ADMIN_PASSWORD` is used for both Google Gemini OAuth login and Chrome Extension WebSocket authentication.
- **Device Authorization:** The Chrome extension includes a device-level permission toggle — Gemini cannot interact with tabs unless you explicitly allow it.
- **No Third-Party Telemetry:** BrowseGemini is 100% self-hosted and independent.

---

## 📄 License

This project is licensed under the [MIT License](LICENSE).
