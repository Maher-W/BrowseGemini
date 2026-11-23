// popup.js - Gemini Browser Connector UI logic

document.addEventListener("DOMContentLoaded", async () => {
  const masterToggle = document.getElementById("masterToggle");
  const toggleDesc = document.getElementById("toggleDescription");
  const statusBadge = document.getElementById("statusBadge");
  const statusText = document.getElementById("statusText");
  const attachedTabsCount = document.getElementById("attachedTabsCount");
  const serverUrlInput = document.getElementById("serverUrl");
  const extensionTokenInput = document.getElementById("extensionToken");
  const toggleTokenBtn = document.getElementById("toggleTokenVisibility");
  const saveBtn = document.getElementById("saveBtn");
  const reconnectBtn = document.getElementById("reconnectBtn");
  const feedbackEl = document.getElementById("saveFeedback");

  // Load saved settings
  const config = await chrome.storage.local.get({
    enabled: false,
    serverUrl: "ws://localhost:8080/ws",
    token: ""
  });

  masterToggle.checked = config.enabled;
  serverUrlInput.value = config.serverUrl;
  extensionTokenInput.value = config.token;

  updateToggleDescription(config.enabled);

  // Ask background worker for live status
  async function refreshStatus() {
    try {
      const response = await chrome.runtime.sendMessage({ type: "getStatus" });
      if (response) {
        renderStatus(response.status, response.attachedCount, response.errorMessage);
      }
    } catch (e) {
      // Background worker might be waking up or off
      if (!masterToggle.checked) {
        renderStatus("off", 0);
      }
    }
  }

  function renderStatus(status, attachedCount = 0, errorMsg = "") {
    statusBadge.className = "status-badge";
    attachedTabsCount.textContent = `${attachedCount} tab${attachedCount === 1 ? "" : "s"} attached`;

    switch (status) {
      case "connected":
        statusBadge.classList.add("status-on");
        statusText.textContent = "ON — Connected";
        break;
      case "connecting":
        statusBadge.classList.add("status-connecting");
        statusText.textContent = "Connecting...";
        break;
      case "error":
        statusBadge.classList.add("status-error");
        statusText.textContent = errorMsg ? `Error: ${errorMsg}` : "Connection Error";
        break;
      case "off":
      default:
        statusBadge.classList.add("status-off");
        statusText.textContent = "OFF — Private";
        break;
    }
  }

  function updateToggleDescription(enabled) {
    if (enabled) {
      toggleDesc.textContent = "Extension is ACTIVE. Gemini can see and interact with authorized browser tabs.";
    } else {
      toggleDesc.textContent = "Extension is disabled. Gemini cannot see or interact with any tabs.";
    }
  }

  function showFeedback(text, isError = false) {
    feedbackEl.textContent = text;
    feedbackEl.style.color = isError ? "#ef4444" : "#10b981";
    setTimeout(() => {
      feedbackEl.textContent = "";
    }, 3000);
  }

  // Toggle token input visibility
  toggleTokenBtn.addEventListener("click", () => {
    const isPassword = extensionTokenInput.type === "password";
    extensionTokenInput.type = isPassword ? "text" : "password";
  });

  // Master switch changed
  masterToggle.addEventListener("change", async () => {
    const isEnabled = masterToggle.checked;
    updateToggleDescription(isEnabled);
    await chrome.storage.local.set({ enabled: isEnabled });

    // Notify background worker
    try {
      const res = await chrome.runtime.sendMessage({
        type: "toggleState",
        enabled: isEnabled
      });
      if (res) {
        renderStatus(res.status, res.attachedCount);
      }
    } catch (e) {
      console.warn("Could not notify background worker:", e);
    }
    refreshStatus();
  });

  // Save Settings button
  saveBtn.addEventListener("click", async () => {
    const serverUrl = serverUrlInput.value.trim();
    const token = extensionTokenInput.value.trim();

    if (!serverUrl) {
      showFeedback("Server URL cannot be empty", true);
      return;
    }

    await chrome.storage.local.set({ serverUrl, token });
    showFeedback("Settings saved!");

    // If enabled, trigger reconnect
    if (masterToggle.checked) {
      chrome.runtime.sendMessage({ type: "reconnect" }).catch(() => {});
      renderStatus("connecting");
    }
  });

  // Reconnect Now button
  reconnectBtn.addEventListener("click", async () => {
    if (!masterToggle.checked) {
      showFeedback("Turn Master Toggle ON first", true);
      return;
    }
    showFeedback("Reconnecting...");
    renderStatus("connecting");
    try {
      await chrome.runtime.sendMessage({ type: "reconnect" });
    } catch (e) {
      console.warn("Reconnect signal error:", e);
    }
    setTimeout(refreshStatus, 600);
  });

  // Periodic poll while popup is open
  refreshStatus();
  const pollInterval = setInterval(refreshStatus, 1500);
  window.addEventListener("unload", () => clearInterval(pollInterval));
});
