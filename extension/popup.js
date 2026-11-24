// popup.js - BrowseGemini UI logic

document.addEventListener("DOMContentLoaded", async () => {
  // Elements: Header & Status
  const statusBadge = document.getElementById("statusBadge");
  const statusText = document.getElementById("statusText");
  const errorBanner = document.getElementById("errorBanner");
  const errorMessage = document.getElementById("errorMessage");

  // Elements: Paired View
  const pairedView = document.getElementById("pairedView");
  const masterToggle = document.getElementById("masterToggle");
  const toggleDesc = document.getElementById("toggleDescription");
  const deviceAllowedToggle = document.getElementById("deviceAllowedToggle");
  const permStatusBadge = document.getElementById("permStatusBadge");
  const permStatusText = document.getElementById("permStatusText");
  const connectedServerHost = document.getElementById("connectedServerHost");
  const attachedTabsCount = document.getElementById("attachedTabsCount");
  const editSettingsBtn = document.getElementById("editSettingsBtn");
  const reconnectBtn = document.getElementById("reconnectBtn");

  // Elements: Setup / Sign-In View
  const setupView = document.getElementById("setupView");
  const setupTitle = document.getElementById("setupTitle");
  const serverUrlInput = document.getElementById("serverUrl");
  const serverPasswordInput = document.getElementById("serverPassword");
  const togglePasswordBtn = document.getElementById("togglePasswordVisibility");
  const setupDeviceAllowed = document.getElementById("setupDeviceAllowed");
  const saveBtn = document.getElementById("saveBtn");
  const cancelBtn = document.getElementById("cancelBtn");
  const saveFeedback = document.getElementById("saveFeedback");

  // URL normalizer: converts domain or http/https to wss://.../ws
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

  function extractHost(urlStr) {
    try {
      const parsed = new URL(urlStr);
      return parsed.hostname || urlStr;
    } catch (e) {
      return urlStr.replace(/^wss?:\/\//i, "").split("/")[0] || urlStr;
    }
  }

  // Load configuration from local storage
  const config = await chrome.storage.local.get({
    enabled: false,
    deviceAllowed: false,
    serverUrl: "",
    token: ""
  });

  const isPaired = !!(config.serverUrl && config.token);

  if (isPaired) {
    showPairedView(config);
  } else {
    showSetupView(false, config);
  }

  function showPairedView(cfg) {
    pairedView.classList.remove("hidden");
    setupView.classList.add("hidden");

    masterToggle.checked = !!cfg.enabled;
    deviceAllowedToggle.checked = !!cfg.deviceAllowed;
    connectedServerHost.textContent = extractHost(cfg.serverUrl);
    updateToggleDescription(cfg.enabled);
    updatePermBadge(cfg.deviceAllowed);
    refreshStatus();
  }

  function showSetupView(allowCancel = false, cfg = {}) {
    setupView.classList.remove("hidden");
    pairedView.classList.add("hidden");

    if (allowCancel) {
      cancelBtn.classList.remove("hidden");
      setupTitle.textContent = "Edit Connection Settings";
    } else {
      cancelBtn.classList.add("hidden");
      setupTitle.textContent = "Server Setup & Sign-In";
    }

    serverUrlInput.value = cfg.serverUrl || "browse.gemini.code.ebda3is.com";
    serverPasswordInput.value = cfg.token || "";
    setupDeviceAllowed.checked = cfg.deviceAllowed !== undefined ? !!cfg.deviceAllowed : true;
  }

  function updateToggleDescription(enabled) {
    if (enabled) {
      toggleDesc.textContent = "Live Gemini control is active. Gemini can view and interact with open tabs.";
    } else {
      toggleDesc.textContent = "Extension is OFF. Gemini cannot view or interact with any tabs.";
    }
  }

  function updatePermBadge(isAllowed) {
    permStatusBadge.className = "perm-status";
    if (isAllowed) {
      permStatusBadge.classList.add("perm-authorized");
      permStatusText.textContent = "Device Authorized ✓";
    } else {
      permStatusBadge.classList.add("perm-blocked");
      permStatusText.textContent = "Access Blocked ⛔";
    }
  }

  function renderStatus(status, attachedCount = 0, errorMsg = "", deviceAllowed = true, enabled = true) {
    statusBadge.className = "status-badge";
    attachedTabsCount.textContent = `${attachedCount} tab${attachedCount === 1 ? "" : "s"} attached`;

    if (!enabled) {
      statusBadge.classList.add("status-off");
      statusText.textContent = "OFF — Private";
      errorBanner.classList.add("hidden");
      return;
    }

    if (!deviceAllowed) {
      statusBadge.classList.add("status-blocked");
      statusText.textContent = "Device Blocked";
      errorBanner.classList.remove("hidden");
      errorMessage.textContent = "Gemini access to this device is toggled OFF. Switch it on below to allow connection.";
      return;
    }

    switch (status) {
      case "connected":
        statusBadge.classList.add("status-on");
        statusText.textContent = "ON — Connected";
        errorBanner.classList.add("hidden");
        break;
      case "connecting":
        statusBadge.classList.add("status-connecting");
        statusText.textContent = "Connecting...";
        errorBanner.classList.add("hidden");
        break;
      case "error":
        statusBadge.classList.add("status-error");
        statusText.textContent = "Connection Error";
        errorBanner.classList.remove("hidden");
        errorMessage.textContent = errorMsg || "Connection failed. Check server URL & password.";
        break;
      case "off":
      default:
        statusBadge.classList.add("status-off");
        statusText.textContent = "OFF";
        errorBanner.classList.add("hidden");
        break;
    }
  }

  async function refreshStatus() {
    try {
      const response = await chrome.runtime.sendMessage({ type: "getStatus" });
      if (response) {
        renderStatus(
          response.status,
          response.attachedCount,
          response.errorMessage,
          response.deviceAllowed,
          response.enabled
        );
      }
    } catch (e) {
      if (!masterToggle.checked) {
        renderStatus("off", 0, "", true, false);
      }
    }
  }

  function showFeedback(text, isError = false) {
    saveFeedback.textContent = text;
    saveFeedback.style.color = isError ? "#ef4444" : "#10b981";
    setTimeout(() => {
      saveFeedback.textContent = "";
    }, 3500);
  }

  // Toggle Password Visibility
  togglePasswordBtn.addEventListener("click", () => {
    const isPassword = serverPasswordInput.type === "password";
    serverPasswordInput.type = isPassword ? "text" : "password";
  });

  // Master switch changed
  masterToggle.addEventListener("change", async () => {
    const isEnabled = masterToggle.checked;
    updateToggleDescription(isEnabled);
    await chrome.storage.local.set({ enabled: isEnabled });

    try {
      await chrome.runtime.sendMessage({ type: "toggleState", enabled: isEnabled });
    } catch (e) {
      console.warn("Could not notify background worker:", e);
    }
    refreshStatus();
  });

  // Device permission switch changed in Paired view
  deviceAllowedToggle.addEventListener("change", async () => {
    const isAllowed = deviceAllowedToggle.checked;
    updatePermBadge(isAllowed);
    await chrome.storage.local.set({ deviceAllowed: isAllowed });

    try {
      await chrome.runtime.sendMessage({ type: "setDeviceAllowed", allowed: isAllowed });
    } catch (e) {
      console.warn("Could not notify background worker:", e);
    }
    refreshStatus();
  });

  // Edit Settings button clicked
  editSettingsBtn.addEventListener("click", async () => {
    const cfg = await chrome.storage.local.get({
      serverUrl: "",
      token: "",
      deviceAllowed: true
    });
    showSetupView(true, cfg);
  });

  // Cancel edit button clicked
  cancelBtn.addEventListener("click", async () => {
    const cfg = await chrome.storage.local.get({
      enabled: false,
      deviceAllowed: false,
      serverUrl: "",
      token: ""
    });
    showPairedView(cfg);
  });

  // Reconnect button
  reconnectBtn.addEventListener("click", async () => {
    if (!masterToggle.checked) {
      showFeedback("Turn Live Gemini Control ON first", true);
      return;
    }
    renderStatus("connecting", 0, "", true, true);
    try {
      await chrome.runtime.sendMessage({ type: "reconnect" });
    } catch (e) {}
    setTimeout(refreshStatus, 800);
  });

  // Save Settings & Connect button
  saveBtn.addEventListener("click", async () => {
    const rawUrl = serverUrlInput.value.trim();
    const password = serverPasswordInput.value.trim();
    const deviceAllowed = setupDeviceAllowed.checked;

    if (!rawUrl) {
      showFeedback("Please enter your server URL or domain", true);
      return;
    }

    if (!password) {
      showFeedback("Please enter your server password", true);
      return;
    }

    if (!deviceAllowed) {
      showFeedback("Please check 'Allow Gemini' to enable connection", true);
      return;
    }

    const normalizedUrl = normalizeWebSocketUrl(rawUrl);

    // Save configuration
    await chrome.storage.local.set({
      serverUrl: normalizedUrl,
      token: password,
      deviceAllowed: true,
      enabled: true
    });

    showFeedback("Saved! Connecting...", false);

    // Switch to paired view immediately
    const updatedCfg = {
      serverUrl: normalizedUrl,
      token: password,
      deviceAllowed: true,
      enabled: true
    };
    showPairedView(updatedCfg);

    // Trigger reconnect
    try {
      await chrome.runtime.sendMessage({ type: "reconnect" });
    } catch (e) {}

    setTimeout(refreshStatus, 600);
  });

  // Poll status while popup is open
  refreshStatus();
  const pollInterval = setInterval(refreshStatus, 1500);
  window.addEventListener("unload", () => clearInterval(pollInterval));
});