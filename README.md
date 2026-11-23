# Gemini Live Chrome Browser MCP Bridge

An ultra-lightweight, production-ready system that connects **Google Gemini** (via [gemini.google.com](https://gemini.google.com) Custom Connected Apps) to your live **Google Chrome** browser over a remote Model Context Protocol (MCP) server and a Manifest V3 Chrome Extension.

Designed specifically for **Coolify** self-hosting with an ultra-minimal footprint (~10MB RAM).

---

## Architecture & Data Flow

```
+-------------------+      HTTPS Streamable HTTP MCP + OAuth 2.0      +----------------------+
|  Google Gemini    |  <===========================================>  |  Go Remote Server    |
| (gemini.google)   |                                                 |  (:8080 on Coolify)  |
+-------------------+                                                 +----------------------+
                                                                                 ^
                                                                                 | WSS WebSocket
                                                                                 | + Secret Token
                                                                                 v
+-------------------+             Chrome DevTools Protocol (CDP)      +----------------------+
| Live Chrome Tabs  |  <===========================================>  |  Chrome Extension    |
| (Active Sessions) |                                                 |  (Manifest V3)       |
+-------------------+                                                 +----------------------+
```

### Key Highlights
- **Ultra-Lightweight (~10MB RAM):** The server is compiled as a pure static Go binary inside an Alpine container. No NodeJS, no Python runtime.
- **Coolify Zero-Config Multiplexing:** HTTP endpoints, OAuth discovery, Streamable HTTP MCP, and WebSockets all listen on port `8080`. Coolify routes both HTTPS and WSS directly to the single container with no custom proxy configuration.
- **Two-Layer Security:**
  - **Gemini OAuth 2.0 (RFC 8414 & RFC 7591):** Dynamic Client Registration + Web Authorization Screen verified against `ADMIN_PASSWORD`.
  - **Extension WebSocket:** Authenticated via `EXTENSION_SECRET_TOKEN` over TLS.
- **Persistent State:** OAuth client registrations and Gemini refresh tokens persist in `/data/oauth_store.json` across server restarts.
- **Privacy First (Master Toggle):** When toggled OFF in the extension popup, the WebSocket closes, Chrome's debugging banner vanishes, all CDP sessions detach, and any incoming Gemini requests are blocked.
- **Hybrid Shortcut Handling:** Native Chrome API handling for browser-level shortcuts (`Ctrl+T`, `Ctrl+W`, `Ctrl+Tab`, `Alt+Left`, `Ctrl+Shift+T`, etc.) and trusted CDP key dispatch for page-level editor shortcuts (`Ctrl+C`, `Ctrl+V`, `Ctrl+A`, etc.).

---

## Directory Structure

```
.
├── server/
│   ├── cmd/server/main.go            # HTTP server, routing, healthcheck, graceful shutdown
│   ├── internal/
│   │   ├── auth/
│   │   │   ├── store.go             # Persistent JSON OAuth store in /data
│   │   │   └── oauth.go             # RFC 8414 metadata, RFC 7591 registration, login form, tokens
│   │   ├── hub/
│   │   │   └── hub.go               # WebSocket connection manager & bidirectional RPC dispatcher
│   │   └── mcp/
│   │       └── server.go            # 10 MCP tools using github.com/modelcontextprotocol/go-sdk
│   ├── Dockerfile                   # Multi-stage static Alpine build
│   ├── go.mod
│   └── go.sum
├── extension/
│   ├── manifest.json                # Manifest V3 configuration
│   ├── background.js                # Service worker, CDP bridge, keep-alive alarms, shortcuts
│   ├── popup.html                   # Master ON/OFF toggle, status badge, connection settings
│   ├── popup.css                    # Modern dark UI
│   ├── popup.js                     # Extension popup controller
│   └── icons/                       # Extension icons (16px, 48px, 128px)
├── .env.example                     # Environment template
└── README.md
```

---

## Coolify Deployment (Step-by-Step)

### 1. Create a New Application in Coolify
1. In your Coolify dashboard, select your Project and Environment.
2. Click **+ New Resource** &rarr; **Git Repository (or Private Git Repository)**.
3. Select your repository.
4. Set **Build Pack** to **Dockerfile**.
5. Set **Base Directory** to `/server` (or set Dockerfile path to `server/Dockerfile`).

### 2. Configure Ports & Healthcheck
- **Exposed Port:** `8080`
- **Domain:** Assign your domain (e.g., `https://mcp.yourdomain.com`).
  - Coolify automatically configures Traefik/Caddy to handle Let's Encrypt SSL and upgrades WebSocket (`wss://`) traffic automatically.
- **Healthcheck Path:** `/health`
- **Healthcheck Interval:** `15s`

### 3. Attach Persistent Volume
To ensure Gemini doesn't get logged out whenever the server redeploys:
1. In your application settings, navigate to **Storages / Persistent Storage**.
2. Add a new volume mount:
   - **Destination path in container:** `/data`
   - **Source volume name:** `gemini-browser-data`

### 4. Configure Environment Variables
Add the following environment variables in Coolify:

| Variable | Example Value | Description |
| :--- | :--- | :--- |
| `PORT` | `8080` | Port the Go server listens on |
| `HOST` | `0.0.0.0` | Bind address inside container |
| `DATA_DIR` | `/data` | Directory for persistent OAuth token storage |
| `BASE_URL` | `https://mcp.yourdomain.com` | Public HTTPS domain for OAuth discovery |
| `ADMIN_PASSWORD` | `MyVerySecurePassword123!` | Password required on web login when authorizing Gemini |
| `EXTENSION_SECRET_TOKEN` | `generate-a-strong-random-token` | Shared secret between Chrome extension and server |

Click **Deploy**. Once built, the container will use ~10MB RAM.

---

## Connecting Google Gemini

1. Open [gemini.google.com](https://gemini.google.com).
2. Go to **Settings** &rarr; **Connected apps** &rarr; **Add custom connected app**.
3. In the MCP Server URL field, enter:
   ```
   https://mcp.yourdomain.com/mcp
   ```
4. Gemini will automatically query:
   `https://mcp.yourdomain.com/.well-known/oauth-authorization-server`
5. Click **Connect**. Gemini opens a pop-up window showing the Authorization screen.
6. Enter your `ADMIN_PASSWORD` and submit.
7. Gemini receives the OAuth token and connects to the MCP tools.

---

## Installing the Chrome Extension

1. Open Google Chrome and go to `chrome://extensions/`.
2. Turn on **Developer mode** in the top-right corner.
3. Click **Load unpacked** and select the `extension/` folder from this repository.
4. The **Gemini Live Browser Connector** extension will appear in your toolbar.
5. Click the extension icon to open the popup:
   - **Server URL:** `wss://mcp.yourdomain.com/ws` (or `ws://localhost:8080/ws` for local dev)
   - **Extension Token:** Enter the `EXTENSION_SECRET_TOKEN` you set in `.env`
   - Click **Save Settings**
   - Toggle the **Live Gemini Control** switch to **ON**.
6. The badge will turn green (**ON — Connected**).

> **Privacy Guarantee:** Whenever you turn the toggle **OFF**, the extension immediately drops the WebSocket, cleans up keep-alive alarms, detaches the CDP debugger from all tabs (removing Chrome's debug bar), and Gemini is completely locked out.

---

## MCP Tools Reference

Gemini has access to 10 browser tools:

### 1. `browser_tabs`
Manage browser tabs.
- **Parameters:**
  - `action`: `"list"` | `"new"` | `"switch"` | `"close"`
  - `tabId` *(optional)*: Target tab ID (required for `switch` and `close`).
  - `url` *(optional)*: URL to open when `action` is `"new"`.
- **Returns:** Tab list or operation confirmation.

### 2. `browser_screenshot`
Captures a viewport screenshot.
- **Parameters:**
  - `tabId` *(optional)*: Target tab ID (defaults to active tab).
- **Behavior:** Captures via CDP `Page.captureScreenshot`, resizes cleanly via OffscreenCanvas (max width 1280px, JPEG quality 80) so coordinates match viewport pixels, and returns MCP image content alongside metadata (`tabId`, `url`, `title`, `width`, `height`).

### 3. `browser_get_elements`
Retrieves visible interactive elements.
- **Parameters:**
  - `tabId` *(optional)*: Target tab ID (defaults to active tab).
- **Behavior:** Injects a lightweight read-only script finding `<a>`, `<button>`, `<input>`, `<textarea>`, `<select>`, `[role="button"]`, and returns their index, tag name, text/placeholder/aria-label, and center `(x, y)` coordinate.

### 4. `browser_click`
Clicks on any viewport coordinate.
- **Parameters:**
  - `x`: Viewport X coordinate.
  - `y`: Viewport Y coordinate.
  - `button` *(optional)*: `"left"` (default) | `"right"` | `"middle"`
  - `clickCount` *(optional)*: `1` (single click, default) | `2` (double click)
  - `tabId` *(optional)*: Target tab ID.
- **Behavior:** Dispatches trusted CDP `Input.dispatchMouseEvent` (`mouseMoved`, `mousePressed`, `mouseReleased`).

### 5. `browser_move_mouse`
Moves cursor position.
- **Parameters:**
  - `x`: Viewport X coordinate.
  - `y`: Viewport Y coordinate.
  - `tabId` *(optional)*: Target tab ID.

### 6. `browser_drag`
Simulates click and drag gesture.
- **Parameters:**
  - `startX`, `startY`: Start coordinates.
  - `endX`, `endY`: Destination coordinates.
  - `tabId` *(optional)*: Target tab ID.

### 7. `browser_scroll`
Scrolls the page or elements.
- **Parameters:**
  - `x` *(optional)*: Scroll origin X (default 640).
  - `y` *(optional)*: Scroll origin Y (default 360).
  - `deltaX` *(optional)*: Horizontal scroll amount.
  - `deltaY` *(optional)*: Vertical scroll amount.
  - `tabId` *(optional)*: Target tab ID.

### 8. `browser_type`
Types text into focused elements.
- **Parameters:**
  - `text`: String to type.
  - `pressEnter` *(optional)*: Boolean. If true, dispatches Enter keydown/keyup after inserting text.
  - `tabId` *(optional)*: Target tab ID.

### 9. `browser_shortcut`
Hybrid keyboard shortcut execution.
- **Parameters:**
  - `shortcut`: e.g. `"Ctrl+C"`, `"Ctrl+V"`, `"Ctrl+A"`, `"Ctrl+T"`, `"Ctrl+W"`, `"Ctrl+Shift+T"`, `"Ctrl+Tab"`, `"Ctrl+R"`, `"Alt+Left"`, `"Enter"`, `"Escape"`, `"ArrowDown"`
- **Behavior:**
  - **Browser-level shortcuts** (`Ctrl+T`, `Ctrl+W`, `Ctrl+Shift+T`, `Ctrl+Tab`, `Ctrl+R`, `Alt+Left`, `Alt+Right`) are intercepted and executed natively via Chrome Extension APIs.
  - **Page-level shortcuts** (`Ctrl+C`, `Ctrl+V`, `Ctrl+A`, `Ctrl+Z`, `Escape`, `Tab`, arrows, etc.) are dispatched via CDP `Input.dispatchKeyEvent` with the proper modifier bitmask and virtual key codes.

### 10. `browser_navigate`
Navigates the tab.
- **Parameters:**
  - `url`: Target URL or `"back"`, `"forward"`, `"reload"`.
  - `tabId` *(optional)*: Target tab ID.

---

## Local Development & Testing

1. **Build and Run Go Server Locally:**
   ```bash
   cd server
   go mod tidy
   go build ./...
   PORT=8080 ADMIN_PASSWORD=admin123 EXTENSION_SECRET_TOKEN=mysecret go run ./cmd/server
   ```
2. **Verify Endpoints:**
   - Healthcheck: `curl http://localhost:8080/health`
   - OAuth Discovery: `curl http://localhost:8080/.well-known/oauth-authorization-server`
3. **Load Extension:** Load unpacked `extension/` in `chrome://extensions/` and connect to `ws://localhost:8080/ws`.
