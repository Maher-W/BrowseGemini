// background.js - BrowseGemini Chrome Extension Service Worker (MV3)

// State
let ws = null;
let currentStatus = "off"; // "off" | "connecting" | "connected" | "error" | "blocked"
let lastErrorMessage = "";
let reconnectTimer = null;
let reconnectAttempts = 0;
let hadHandshakeError = false;
const attachedTabs = new Map(); // tabId -> boolean

// Initialize on service worker startup
chrome.runtime.onStartup.addListener(() => {
  initFromStorage();
});

chrome.runtime.onInstalled.addListener(() => {
  initFromStorage();
});

// URL Normalizer: converts domain or http/https to wss://.../ws
function normalizeWebSocketUrl(rawUrl) {
  let u = (rawUrl || "").trim();
  if (!u) return "";
  if (u.startsWith("https://")) {
    u = "wss://" + u.slice(8);
  } else if (u.startsWith("http://")) {
    u = "ws://" + u.slice(7);
  } else if (!u.startsWith("ws://") && !u.startsWith("wss://")) {
    u = "wss://" + u;
  }
  try {
    const parsed = new URL(u);
    if (!parsed.pathname || parsed.pathname === "/" || parsed.pathname === "/mcp") {
      parsed.pathname = "/ws";
    }
    return parsed.toString();
  } catch (e) {
    return u;
  }
}

async function initFromStorage() {
  const config = await chrome.storage.local.get({
    enabled: false,
    deviceAllowed: false,
    serverUrl: "",
    token: ""
  });

  if (config.enabled && config.deviceAllowed && config.serverUrl) {
    connectWebSocket(config.serverUrl, config.token);
    startKeepAlive();
  } else {
    currentStatus = config.enabled && !config.deviceAllowed ? "blocked" : "off";
    stopKeepAlive();
    detachAllDebuggers();
  }
}

// Keep-alive using chrome.alarms (MV3 wakes up when alarm fires)
function startKeepAlive() {
  chrome.alarms.create("geminiKeepAlive", { periodInMinutes: 0.33 }); // ~20 seconds
}

function stopKeepAlive() {
  chrome.alarms.clear("geminiKeepAlive");
}

chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === "geminiKeepAlive") {
    if (ws && ws.readyState === WebSocket.OPEN) {
      try {
        ws.send(JSON.stringify({ type: "ping", timestamp: Date.now() }));
      } catch (e) {
        console.warn("Error sending ping frame:", e);
      }
    }
  }
});

// Clean up debugger sessions if tab is closed or detached
chrome.tabs.onRemoved.addListener((tabId) => {
  attachedTabs.delete(tabId);
});

chrome.debugger.onDetach.addListener((source, reason) => {
  if (source.tabId) {
    attachedTabs.delete(source.tabId);
    console.log(`Debugger detached from tab ${source.tabId}: ${reason}`);
  }
});

// Detach all debuggers immediately
async function detachAllDebuggers() {
  const tabsToDetach = Array.from(attachedTabs.keys());
  attachedTabs.clear();
  for (const tabId of tabsToDetach) {
    try {
      await chrome.debugger.detach({ tabId });
    } catch (e) {
      // Tab might already be closed or detached
    }
  }
}

// Ensure CDP debugger is attached to target tab
async function ensureDebuggerAttached(tabId) {
  if (attachedTabs.get(tabId)) {
    return true;
  }

  try {
    await chrome.debugger.attach({ tabId }, "1.3");
    // Enable essential CDP domains
    await chrome.debugger.sendCommand({ tabId }, "Page.enable");
    await chrome.debugger.sendCommand({ tabId }, "Runtime.enable");
    await chrome.debugger.sendCommand({ tabId }, "DOM.enable");
    attachedTabs.set(tabId, true);
    return true;
  } catch (err) {
    console.error(`Failed to attach debugger to tab ${tabId}:`, err);
    throw new Error(`Could not attach debugger to tab ${tabId}: ${err.message}`);
  }
}

// Helper to resolve the target tab
async function resolveTargetTab(tabId) {
  if (tabId && typeof tabId === "number") {
    try {
      const tab = await chrome.tabs.get(tabId);
      if (tab) return tab;
    } catch (e) {
      // Fall through to active tab
    }
  }

  // Find active tab in current or last focused window
  const activeTabs = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  if (activeTabs.length > 0) {
    return activeTabs[0];
  }

  // Fallback to any active tab
  const anyActive = await chrome.tabs.query({ active: true });
  if (anyActive.length > 0) {
    return anyActive[0];
  }

  throw new Error("No active browser tab found.");
}

// WebSocket Connection Manager
async function connectWebSocket(serverUrl, token) {
  const { enabled, deviceAllowed } = await chrome.storage.local.get({
    enabled: false,
    deviceAllowed: false
  });

  if (!enabled) {
    currentStatus = "off";
    lastErrorMessage = "";
    if (ws) {
      try { ws.close(); } catch (e) {}
      ws = null;
    }
    detachAllDebuggers();
    return;
  }

  if (!deviceAllowed) {
    currentStatus = "blocked";
    lastErrorMessage = "Device not authorized. Check 'Allow Gemini' in settings.";
    if (ws) {
      try { ws.close(); } catch (e) {}
      ws = null;
    }
    detachAllDebuggers();
    return;
  }

  if (ws) {
    try {
      ws.onclose = null;
      ws.onerror = null;
      ws.close();
    } catch (e) {}
    ws = null;
  }

  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }

  currentStatus = "connecting";
  lastErrorMessage = "";
  hadHandshakeError = false;

  const normalizedUrl = normalizeWebSocketUrl(serverUrl);
  let finalUrl = normalizedUrl;
  try {
    const parsed = new URL(normalizedUrl);
    if (token) {
      parsed.searchParams.set("token", token);
    }
    finalUrl = parsed.toString();
  } catch (e) {
    currentStatus = "error";
    lastErrorMessage = "Invalid server URL format";
    return;
  }

  try {
    ws = new WebSocket(finalUrl);
  } catch (e) {
    currentStatus = "error";
    lastErrorMessage = e.message || "Failed to create WebSocket connection";
    scheduleReconnect();
    return;
  }

  ws.onopen = () => {
    console.log("WebSocket connected to MCP server:", normalizedUrl);
    currentStatus = "connected";
    lastErrorMessage = "";
    reconnectAttempts = 0;
    hadHandshakeError = false;

    // Send hello handshake
    ws.send(JSON.stringify({
      type: "handshake",
      role: "chrome-extension",
      version: "1.0.0",
      timestamp: Date.now()
    }));
  };

  ws.onmessage = async (event) => {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch (e) {
      console.error("Invalid JSON message from server:", event.data);
      return;
    }

    if (msg.type === "ping") {
      ws.send(JSON.stringify({ type: "pong", timestamp: Date.now() }));
      return;
    }

    if (msg.type === "pong") {
      return;
    }

    if (msg.id && msg.action) {
      handleToolRequest(msg);
    }
  };

  ws.onerror = (err) => {
    console.warn("WebSocket error:", err);
    hadHandshakeError = true;
    currentStatus = "error";
    lastErrorMessage = "Connection rejected. Verify server URL and password.";
  };

  ws.onclose = (event) => {
    console.log(`WebSocket closed: code=${event.code}, reason=${event.reason}`);
    ws = null;
    chrome.storage.local.get({ enabled: false, deviceAllowed: false }, (cfg) => {
      if (!cfg.enabled) {
        currentStatus = "off";
        lastErrorMessage = "";
        detachAllDebuggers();
        return;
      }
      if (!cfg.deviceAllowed) {
        currentStatus = "blocked";
        lastErrorMessage = "Device access not allowed.";
        detachAllDebuggers();
        return;
      }

      if (hadHandshakeError) {
        currentStatus = "error";
      } else {
        currentStatus = "connecting";
      }
      scheduleReconnect();
    });
  };
}

function scheduleReconnect() {
  chrome.storage.local.get({ enabled: false, deviceAllowed: false, serverUrl: "", token: "" }, (cfg) => {
    if (!cfg.enabled || !cfg.deviceAllowed) {
      return;
    }

    reconnectAttempts++;
    const delay = Math.min(1500 * Math.pow(1.5, reconnectAttempts - 1), 30000);
    console.log(`Scheduling reconnect attempt ${reconnectAttempts} in ${Math.round(delay)}ms...`);

    if (reconnectTimer) clearTimeout(reconnectTimer);
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      connectWebSocket(cfg.serverUrl, cfg.token);
    }, delay);
  });
}

// Tool Request Router & Execution
async function handleToolRequest(request) {
  const { id, action, params = {} } = request;

  try {
    const { enabled, deviceAllowed } = await chrome.storage.local.get({
      enabled: false,
      deviceAllowed: false
    });

    if (!enabled) {
      throw new Error("BrowseGemini is currently toggled OFF in Chrome.");
    }

    if (!deviceAllowed) {
      throw new Error("Gemini is not authorized to connect to this device. Please allow access in the BrowseGemini extension.");
    }

    let result;
    switch (action) {
      case "browser_tabs":
        result = await handleBrowserTabs(params);
        break;
      case "browser_screenshot":
        result = await handleBrowserScreenshot(params);
        break;
      case "browser_get_elements":
        result = await handleBrowserGetElements(params);
        break;
      case "browser_click":
        result = await handleBrowserClick(params);
        break;
      case "browser_move_mouse":
        result = await handleBrowserMoveMouse(params);
        break;
      case "browser_drag":
        result = await handleBrowserDrag(params);
        break;
      case "browser_scroll":
        result = await handleBrowserScroll(params);
        break;
      case "browser_type":
        result = await handleBrowserType(params);
        break;
      case "browser_shortcut":
        result = await handleBrowserShortcut(params);
        break;
      case "browser_navigate":
        result = await handleBrowserNavigate(params);
        break;
      default:
        throw new Error(`Unknown browser action: ${action}`);
    }

    sendResponse(id, { success: true, data: result });
  } catch (err) {
    console.error(`Error executing action ${action}:`, err);
    sendResponse(id, { success: false, error: err.message || String(err) });
  }
}

function sendResponse(id, payload) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ id, ...payload }));
  }
}

// -------------------------------------------------------------
// Tool Implementations
// -------------------------------------------------------------

// 1. browser_tabs: list | new | switch | close
async function handleBrowserTabs(params) {
  const { action = "list", tabId, url } = params;

  if (action === "list") {
    const tabs = await chrome.tabs.query({});
    return tabs.map(t => ({
      id: t.id,
      title: t.title || "",
      url: t.url || "",
      active: !!t.active,
      windowId: t.windowId,
      pinned: !!t.pinned
    }));
  }

  if (action === "new") {
    const newTab = await chrome.tabs.create({
      url: url || "about:blank",
      active: true
    });
    return {
      tabId: newTab.id,
      url: newTab.url || url || "about:blank",
      title: newTab.title || "",
      active: true
    };
  }

  if (action === "switch") {
    if (!tabId) throw new Error("tabId is required for 'switch' action");
    const updated = await chrome.tabs.update(tabId, { active: true });
    if (updated.windowId) {
      await chrome.windows.update(updated.windowId, { focused: true });
    }
    return { tabId: updated.id, title: updated.title, url: updated.url, active: true };
  }

  if (action === "close") {
    if (!tabId) throw new Error("tabId is required for 'close' action");
    if (attachedTabs.has(tabId)) {
      try {
        await chrome.debugger.detach({ tabId });
      } catch (e) {}
      attachedTabs.delete(tabId);
    }
    await chrome.tabs.remove(tabId);
    return { closedTabId: tabId, success: true };
  }

  throw new Error(`Unknown tabs action: ${action}`);
}

// 2. browser_screenshot: captures CDP screenshot, ensures viewport match & max width 1280px
async function handleBrowserScreenshot(params) {
  const targetTab = await resolveTargetTab(params.tabId);
  await ensureDebuggerAttached(targetTab.id);

  const result = await chrome.debugger.sendCommand(
    { tabId: targetTab.id },
    "Page.captureScreenshot",
    { format: "jpeg", quality: 80 }
  );

  let base64Data = result.data;

  const layout = await chrome.debugger.sendCommand(
    { tabId: targetTab.id },
    "Page.getLayoutMetrics"
  );
  
  let width = layout.cssVisualViewport ? layout.cssVisualViewport.clientWidth : 1280;
  let height = layout.cssVisualViewport ? layout.cssVisualViewport.clientHeight : 720;

  try {
    const binaryStr = atob(base64Data);
    const bytes = new Uint8Array(binaryStr.length);
    for (let i = 0; i < binaryStr.length; i++) {
      bytes[i] = binaryStr.charCodeAt(i);
    }
    const blob = new Blob([bytes], { type: "image/jpeg" });
    const bitmap = await createImageBitmap(blob);

    let curWidth = bitmap.width;
    let curHeight = bitmap.height;
    const maxWidth = 1280;

    if (curWidth > maxWidth) {
      const scale = maxWidth / curWidth;
      const targetW = maxWidth;
      const targetH = Math.round(curHeight * scale);

      const canvas = new OffscreenCanvas(targetW, targetH);
      const ctx = canvas.getContext("2d");
      ctx.drawImage(bitmap, 0, 0, targetW, targetH);
      const resizedBlob = await canvas.convertToBlob({ type: "image/jpeg", quality: 0.8 });

      const reader = new FileReader();
      base64Data = await new Promise((resolve, reject) => {
        reader.onloadend = () => {
          const res = reader.result;
          resolve(res.split(",")[1]);
        };
        reader.onerror = reject;
        reader.readAsDataURL(resizedBlob);
      });

      width = targetW;
      height = targetH;
    } else {
      width = curWidth;
      height = curHeight;
    }
  } catch (canvasErr) {
    console.warn("OffscreenCanvas resize skipped or failed:", canvasErr);
  }

  return {
    tabId: targetTab.id,
    url: targetTab.url,
    title: targetTab.title,
    width: Math.round(width),
    height: Math.round(height),
    image: base64Data
  };
}

// 3. browser_get_elements: returns visible interactive elements with center (x, y)
async function handleBrowserGetElements(params) {
  const targetTab = await resolveTargetTab(params.tabId);
  await ensureDebuggerAttached(targetTab.id);

  const evaluationScript = `(() => {
    const selectors = [
      'a[href]', 'button', 'input', 'textarea', 'select',
      '[role="button"]', '[role="link"]', '[role="checkbox"]',
      '[role="menuitem"]', '[role="tab"]', '[role="combobox"]',
      '[onclick]', '[tabindex]:not([tabindex="-1"])'
    ];
    const elements = document.querySelectorAll(selectors.join(', '));
    const results = [];
    let idx = 0;

    for (const el of elements) {
      const rect = el.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0) continue;
      if (rect.bottom < 0 || rect.top > window.innerHeight) continue;
      if (rect.right < 0 || rect.left > window.innerWidth) continue;

      const style = window.getComputedStyle(el);
      if (style.visibility === 'hidden' || style.display === 'none' || style.opacity === '0') continue;

      let text = el.innerText || el.textContent || '';
      if (!text.trim()) {
        text = el.getAttribute('aria-label') || el.getAttribute('placeholder') || el.getAttribute('title') || el.value || el.name || '';
      }
      text = text.replace(/\\s+/g, ' ').trim().slice(0, 100);

      const centerX = Math.round(rect.left + rect.width / 2);
      const centerY = Math.round(rect.top + rect.height / 2);

      results.push({
        index: idx++,
        tag: el.tagName.toLowerCase(),
        text: text,
        x: centerX,
        y: centerY,
        width: Math.round(rect.width),
        height: Math.round(rect.height),
        type: el.type || undefined,
        role: el.getAttribute('role') || undefined,
        disabled: el.disabled || el.getAttribute('aria-disabled') === 'true'
      });
      if (results.length >= 150) break;
    }
    return results;
  })()`;

  const evalResult = await chrome.debugger.sendCommand(
    { tabId: targetTab.id },
    "Runtime.evaluate",
    {
      expression: evaluationScript,
      returnByValue: true
    }
  );

  if (evalResult.exceptionDetails) {
    throw new Error(`Script evaluation error: ${evalResult.exceptionDetails.text}`);
  }

  return {
    tabId: targetTab.id,
    url: targetTab.url,
    elements: evalResult.result.value || []
  };
}

// 4. browser_click: trusted Input.dispatchMouseEvent
async function handleBrowserClick(params) {
  const { x, y, button = "left", clickCount = 1 } = params;
  if (x === undefined || y === undefined) {
    throw new Error("x and y coordinates are required for browser_click");
  }

  const targetTab = await resolveTargetTab(params.tabId);
  await ensureDebuggerAttached(targetTab.id);

  const btn = button.toLowerCase();
  const cCount = clickCount || 1;

  await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchMouseEvent", {
    type: "mouseMoved",
    x,
    y
  });

  await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchMouseEvent", {
    type: "mousePressed",
    x,
    y,
    button: btn,
    clickCount: cCount
  });

  await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchMouseEvent", {
    type: "mouseReleased",
    x,
    y,
    button: btn,
    clickCount: cCount
  });

  if (cCount === 2) {
    await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchMouseEvent", {
      type: "mousePressed",
      x,
      y,
      button: btn,
      clickCount: 2
    });
    await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchMouseEvent", {
      type: "mouseReleased",
      x,
      y,
      button: btn,
      clickCount: 2
    });
  }

  return { clicked: true, x, y, button: btn, clickCount: cCount };
}

// 5. browser_move_mouse
async function handleBrowserMoveMouse(params) {
  const { x, y } = params;
  if (x === undefined || y === undefined) {
    throw new Error("x and y are required for browser_move_mouse");
  }

  const targetTab = await resolveTargetTab(params.tabId);
  await ensureDebuggerAttached(targetTab.id);

  await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchMouseEvent", {
    type: "mouseMoved",
    x,
    y
  });

  return { moved: true, x, y };
}

// 6. browser_drag
async function handleBrowserDrag(params) {
  const { startX, startY, endX, endY } = params;
  if (startX === undefined || startY === undefined || endX === undefined || endY === undefined) {
    throw new Error("startX, startY, endX, endY are required for browser_drag");
  }

  const targetTab = await resolveTargetTab(params.tabId);
  await ensureDebuggerAttached(targetTab.id);

  await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchMouseEvent", {
    type: "mouseMoved",
    x: startX,
    y: startY
  });

  await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchMouseEvent", {
    type: "mousePressed",
    x: startX,
    y: startY,
    button: "left",
    clickCount: 1
  });

  const steps = 8;
  for (let i = 1; i <= steps; i++) {
    const curX = Math.round(startX + ((endX - startX) * i) / steps);
    const curY = Math.round(startY + ((endY - startY) * i) / steps);
    await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchMouseEvent", {
      type: "mouseMoved",
      x: curX,
      y: curY,
      button: "left"
    });
  }

  await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchMouseEvent", {
    type: "mouseReleased",
    x: endX,
    y: endY,
    button: "left",
    clickCount: 1
  });

  return { dragged: true, from: { x: startX, y: startY }, to: { x: endX, y: endY } };
}

// 7. browser_scroll
async function handleBrowserScroll(params) {
  const { x = 640, y = 360, deltaX = 0, deltaY = 0 } = params;

  const targetTab = await resolveTargetTab(params.tabId);
  await ensureDebuggerAttached(targetTab.id);

  await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchMouseEvent", {
    type: "mouseWheel",
    x,
    y,
    deltaX,
    deltaY
  });

  return { scrolled: true, x, y, deltaX, deltaY };
}

// 8. browser_type
async function handleBrowserType(params) {
  const { text, pressEnter = false } = params;
  if (text === undefined) {
    throw new Error("text is required for browser_type");
  }

  const targetTab = await resolveTargetTab(params.tabId);
  await ensureDebuggerAttached(targetTab.id);

  await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.insertText", {
    text: String(text)
  });

  if (pressEnter) {
    await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchKeyEvent", {
      type: "rawKeyDown",
      windowsVirtualKeyCode: 13,
      key: "Enter",
      code: "Enter"
    });
    await chrome.debugger.sendCommand({ tabId: targetTab.id }, "Input.dispatchKeyEvent", {
      type: "keyUp",
      windowsVirtualKeyCode: 13,
      key: "Enter",
      code: "Enter"
    });
  }

  return { typed: true, textLength: text.length, pressedEnter: pressEnter };
}

// 9. browser_shortcut
async function handleBrowserShortcut(params) {
  const { shortcut } = params;
  if (!shortcut || typeof shortcut !== "string") {
    throw new Error("shortcut string is required (e.g. 'Ctrl+C', 'Ctrl+T', 'Enter')");
  }

  const targetTab = await resolveTargetTab(params.tabId);
  const normalized = shortcut.trim();

  const browserAction = await handleNativeBrowserShortcut(normalized, targetTab);
  if (browserAction.handled) {
    return {
      shortcut: normalized,
      executedVia: "chrome_native_api",
      result: browserAction.result
    };
  }

  await ensureDebuggerAttached(targetTab.id);
  const cdpResult = await dispatchCDPShortcut(targetTab.id, normalized);
  return {
    shortcut: normalized,
    executedVia: "cdp_dispatchKeyEvent",
    result: cdpResult
  };
}

async function handleNativeBrowserShortcut(shortcut, targetTab) {
  const s = shortcut.toLowerCase().replace(/\s+/g, "");

  if (s === "ctrl+t" || s === "cmd+t" || s === "meta+t") {
    const newTab = await chrome.tabs.create({ active: true });
    return { handled: true, result: { action: "newTab", tabId: newTab.id } };
  }

  if (s === "ctrl+w" || s === "cmd+w" || s === "meta+w") {
    await chrome.tabs.remove(targetTab.id);
    return { handled: true, result: { action: "closeTab", closedTabId: targetTab.id } };
  }

  if (s === "ctrl+shift+t" || s === "cmd+shift+t" || s === "meta+shift+t") {
    if (chrome.sessions && chrome.sessions.restore) {
      const restored = await chrome.sessions.restore();
      return { handled: true, result: { action: "restoreTab", restored } };
    }
  }

  if (s === "ctrl+tab") {
    const tabs = await chrome.tabs.query({ windowId: targetTab.windowId });
    if (tabs.length > 1) {
      const currentIdx = tabs.findIndex(t => t.id === targetTab.id);
      const nextIdx = (currentIdx + 1) % tabs.length;
      await chrome.tabs.update(tabs[nextIdx].id, { active: true });
      return { handled: true, result: { action: "cycleTabNext", activeTabId: tabs[nextIdx].id } };
    }
  }

  if (s === "ctrl+shift+tab") {
    const tabs = await chrome.tabs.query({ windowId: targetTab.windowId });
    if (tabs.length > 1) {
      const currentIdx = tabs.findIndex(t => t.id === targetTab.id);
      const prevIdx = (currentIdx - 1 + tabs.length) % tabs.length;
      await chrome.tabs.update(tabs[prevIdx].id, { active: true });
      return { handled: true, result: { action: "cycleTabPrev", activeTabId: tabs[prevIdx].id } };
    }
  }

  if (s === "ctrl+r" || s === "cmd+r" || s === "f5") {
    await chrome.tabs.reload(targetTab.id);
    return { handled: true, result: { action: "reloadTab", tabId: targetTab.id } };
  }

  if (s === "alt+left" || s === "cmd+left") {
    await chrome.tabs.goBack(targetTab.id);
    return { handled: true, result: { action: "goBack", tabId: targetTab.id } };
  }

  if (s === "alt+right" || s === "cmd+right") {
    await chrome.tabs.goForward(targetTab.id);
    return { handled: true, result: { action: "goForward", tabId: targetTab.id } };
  }

  return { handled: false };
}

async function dispatchCDPShortcut(tabId, shortcut) {
  const parts = shortcut.split("+").map(p => p.trim());
  let modifiers = 0; // Alt=1, Ctrl=2, Meta=4, Shift=8
  let key = "";

  for (const part of parts) {
    const p = part.toLowerCase();
    if (p === "alt") modifiers |= 1;
    else if (p === "ctrl" || p === "control") modifiers |= 2;
    else if (p === "meta" || p === "cmd" || p === "command") modifiers |= 4;
    else if (p === "shift") modifiers |= 8;
    else {
      key = part;
    }
  }

  const keyMapping = getKeyMetadata(key);

  await chrome.debugger.sendCommand({ tabId }, "Input.dispatchKeyEvent", {
    type: "rawKeyDown",
    modifiers,
    windowsVirtualKeyCode: keyMapping.keyCode,
    key: keyMapping.key,
    code: keyMapping.code
  });

  await chrome.debugger.sendCommand({ tabId }, "Input.dispatchKeyEvent", {
    type: "keyUp",
    modifiers,
    windowsVirtualKeyCode: keyMapping.keyCode,
    key: keyMapping.key,
    code: keyMapping.code
  });

  return { key: keyMapping.key, keyCode: keyMapping.keyCode, modifiers };
}

function getKeyMetadata(keyStr) {
  const k = keyStr.toUpperCase();
  if (k.length === 1 && k >= "A" && k <= "Z") {
    return { key: keyStr, code: `Key${k}`, keyCode: k.charCodeAt(0) };
  }
  if (k.length === 1 && k >= "0" && k <= "9") {
    return { key: keyStr, code: `Digit${k}`, keyCode: k.charCodeAt(0) };
  }

  const specialKeys = {
    "ENTER": { key: "Enter", code: "Enter", keyCode: 13 },
    "RETURN": { key: "Enter", code: "Enter", keyCode: 13 },
    "ESCAPE": { key: "Escape", code: "Escape", keyCode: 27 },
    "ESC": { key: "Escape", code: "Escape", keyCode: 27 },
    "TAB": { key: "Tab", code: "Tab", keyCode: 9 },
    "BACKSPACE": { key: "Backspace", code: "Backspace", keyCode: 8 },
    "DELETE": { key: "Delete", code: "Delete", keyCode: 46 },
    "SPACE": { key: " ", code: "Space", keyCode: 32 },
    "ARROWUP": { key: "ArrowUp", code: "ArrowUp", keyCode: 38 },
    "UP": { key: "ArrowUp", code: "ArrowUp", keyCode: 38 },
    "ARROWDOWN": { key: "ArrowDown", code: "ArrowDown", keyCode: 40 },
    "DOWN": { key: "ArrowDown", code: "ArrowDown", keyCode: 40 },
    "ARROWLEFT": { key: "ArrowLeft", code: "ArrowLeft", keyCode: 37 },
    "LEFT": { key: "ArrowLeft", code: "ArrowLeft", keyCode: 37 },
    "ARROWRIGHT": { key: "ArrowRight", code: "ArrowRight", keyCode: 39 },
    "RIGHT": { key: "ArrowRight", code: "ArrowRight", keyCode: 39 },
    "PAGEUP": { key: "PageUp", code: "PageUp", keyCode: 33 },
    "PAGEDOWN": { key: "PageDown", code: "PageDown", keyCode: 34 },
    "HOME": { key: "Home", code: "Home", keyCode: 36 },
    "END": { key: "End", code: "End", keyCode: 35 },
    "F1": { key: "F1", code: "F1", keyCode: 112 },
    "F2": { key: "F2", code: "F2", keyCode: 113 },
    "F3": { key: "F3", code: "F3", keyCode: 114 },
    "F4": { key: "F4", code: "F4", keyCode: 115 },
    "F5": { key: "F5", code: "F5", keyCode: 116 },
    "F6": { key: "F6", code: "F6", keyCode: 117 },
    "F7": { key: "F7", code: "F7", keyCode: 118 },
    "F8": { key: "F8", code: "F8", keyCode: 119 },
    "F9": { key: "F9", code: "F9", keyCode: 120 },
    "F10": { key: "F10", code: "F10", keyCode: 121 },
    "F11": { key: "F11", code: "F11", keyCode: 122 },
    "F12": { key: "F12", code: "F12", keyCode: 123 }
  };

  if (specialKeys[k]) {
    return specialKeys[k];
  }

  return { key: keyStr, code: keyStr, keyCode: keyStr.charCodeAt(0) || 0 };
}

// 10. browser_navigate
async function handleBrowserNavigate(params) {
  const { url } = params;
  if (!url) throw new Error("url is required for browser_navigate");

  const targetTab = await resolveTargetTab(params.tabId);
  const action = url.toLowerCase().trim();

  if (action === "back") {
    await chrome.tabs.goBack(targetTab.id);
    return { navigated: "back", tabId: targetTab.id };
  }

  if (action === "forward") {
    await chrome.tabs.goForward(targetTab.id);
    return { navigated: "forward", tabId: targetTab.id };
  }

  if (action === "reload") {
    await chrome.tabs.reload(targetTab.id);
    return { navigated: "reload", tabId: targetTab.id };
  }

  let targetUrl = url;
  if (!/^https?:\/\//i.test(targetUrl) && !/^about:/i.test(targetUrl) && !/^chrome:\/\//i.test(targetUrl)) {
    targetUrl = "https://" + targetUrl;
  }

  await chrome.tabs.update(targetTab.id, { url: targetUrl });
  return { navigated: targetUrl, tabId: targetTab.id };
}

// -------------------------------------------------------------
// Communication with Popup UI
// -------------------------------------------------------------
chrome.runtime.onMessage.addListener((request, sender, sendResponse) => {
  if (request.type === "getStatus") {
    chrome.storage.local.get({
      enabled: false,
      deviceAllowed: false,
      serverUrl: "",
      token: ""
    }, (cfg) => {
      sendResponse({
        status: currentStatus,
        attachedCount: attachedTabs.size,
        errorMessage: lastErrorMessage,
        enabled: cfg.enabled,
        deviceAllowed: cfg.deviceAllowed,
        paired: !!(cfg.serverUrl && cfg.token)
      });
    });
    return true;
  }

  if (request.type === "toggleState") {
    chrome.storage.local.set({ enabled: request.enabled }, () => {
      if (request.enabled) {
        chrome.storage.local.get({ serverUrl: "", token: "", deviceAllowed: false }, (cfg) => {
          if (cfg.deviceAllowed && cfg.serverUrl) {
            connectWebSocket(cfg.serverUrl, cfg.token);
            startKeepAlive();
          } else {
            currentStatus = !cfg.deviceAllowed ? "blocked" : "off";
          }
          sendResponse({ status: currentStatus, attachedCount: attachedTabs.size });
        });
      } else {
        currentStatus = "off";
        lastErrorMessage = "";
        stopKeepAlive();
        if (ws) {
          try { ws.close(); } catch (e) {}
          ws = null;
        }
        detachAllDebuggers();
        sendResponse({ status: "off", attachedCount: 0 });
      }
    });
    return true;
  }

  if (request.type === "setDeviceAllowed") {
    chrome.storage.local.set({ deviceAllowed: request.allowed }, () => {
      chrome.storage.local.get({ enabled: false, serverUrl: "", token: "" }, (cfg) => {
        if (request.allowed && cfg.enabled && cfg.serverUrl) {
          connectWebSocket(cfg.serverUrl, cfg.token);
          startKeepAlive();
        } else if (!request.allowed) {
          currentStatus = "blocked";
          lastErrorMessage = "Device access not allowed.";
          stopKeepAlive();
          if (ws) {
            try { ws.close(); } catch (e) {}
            ws = null;
          }
          detachAllDebuggers();
        }
        sendResponse({ status: currentStatus });
      });
    });
    return true;
  }

  if (request.type === "reconnect") {
    chrome.storage.local.get({ serverUrl: "", token: "" }, (cfg) => {
      if (cfg.serverUrl) {
        connectWebSocket(cfg.serverUrl, cfg.token);
      }
      sendResponse({ status: currentStatus });
    });
    return true;
  }
});