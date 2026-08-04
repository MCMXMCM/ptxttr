import {
  app,
  BrowserWindow,
  clipboard,
  ipcMain,
  Menu,
  nativeImage,
  powerMonitor,
  protocol,
  session,
  shell,
  systemPreferences,
} from "electron";
import { randomBytes } from "node:crypto";
import { spawn } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  DEFAULT_PORT,
  PRODUCT_NAME,
  TAB_GROUP_ID,
  classifyNavigation,
  loopbackOrigin,
  shouldOpenInBackground,
  swipeNavigation,
} from "./policy.mjs";

const desktopDir = path.dirname(fileURLToPath(import.meta.url));
const brandIconDir = path.join(desktopDir, "..", "web", "static", "img");

function brandIconPath(scheme) {
  return path.join(brandIconDir, `ascritch_icon_${scheme}.png`);
}

const startupPath = path.join(desktopDir, "startup.html");
const startupAssets = new Map([
  ["/index.html", { body: fs.readFileSync(startupPath), contentType: "text/html; charset=utf-8" }],
  ["/icon-light.png", { body: fs.readFileSync(brandIconPath("black")), contentType: "image/png" }],
  ["/icon-dark.png", { body: fs.readFileSync(brandIconPath("white")), contentType: "image/png" }],
]);
const port = DEFAULT_PORT;
const origin = loopbackOrigin(port);
const activityToken = randomBytes(32).toString("hex");
const windows = new Set();
let sidecar = null;
let sidecarLogFD = null;
let quitting = false;
let restarting = false;
let suspended = false;
let appSession = null;
let pendingApplicationWindows = 0;
let startupStatus = "The local server is starting…";

function developmentIconPath() {
  return brandIconPath("black");
}

function applyDevelopmentDockIcon() {
  if (process.platform !== "darwin" || app.isPackaged) return;
  const iconPath = developmentIconPath();
  if (!fs.existsSync(iconPath)) {
    appendLog(`development app icon is missing: ${iconPath}`);
    return;
  }
  const developmentIcon = nativeImage.createFromPath(iconPath);
  if (developmentIcon.isEmpty()) {
    appendLog(`development app icon could not be decoded: ${iconPath}`);
    return;
  }
  // This must run after the first native window is created. Forge runs the
  // unpackaged app inside Electron.app; assigning the Dock icon before that
  // registration is overwritten by Electron's bundled icon on macOS.
  app.dock.setIcon(developmentIcon);
  appendLog(`development Dock icon applied: ${iconPath}`);
}

// Keep the native traffic lights while letting the title bar blend into the
// app, like a normal modern macOS application.  This is deliberately native
// chrome rather than a renderer-drawn imitation, so window dragging, tabs,
// fullscreen, and inactive-window appearance remain system-consistent.
const macOSWindowChrome = process.platform === "darwin"
  ? {
      titleBarStyle: "hiddenInset",
      // The renderer leaves only its sidebar pane transparent, so AppKit's
      // sidebar material is visible there while the reading surface remains
      // fully opaque.
      vibrancy: "sidebar",
      visualEffectState: "followWindow",
      backgroundColor: "#00000000",
    }
  : {};

protocol.registerSchemesAsPrivileged([{
  scheme: "ptxt-startup",
  privileges: { secure: true, standard: true },
}]);

app.setName(PRODUCT_NAME);
if (process.env.PTXT_ELECTRON_USER_DATA) {
  app.setPath("userData", path.resolve(process.env.PTXT_ELECTRON_USER_DATA));
} else {
  app.setPath("userData", path.join(app.getPath("appData"), PRODUCT_NAME));
}
if (process.env.PTXT_ELECTRON_LOG_DIR) {
  app.setPath("logs", path.resolve(process.env.PTXT_ELECTRON_LOG_DIR));
}

const hasSingleInstanceLock = app.requestSingleInstanceLock();
if (!hasSingleInstanceLock) {
  app.exit(0);
} else {
  app.on("second-instance", () => {
    requestApplicationWindow();
  });

  app.on("before-quit", (event) => {
    if (quitting) return;
    event.preventDefault();
    quitting = true;
    void stopSidecar().finally(() => app.exit(0));
  });

  app.on("window-all-closed", () => app.quit());

  await app.whenReady();

  enableMacOSHistorySwipeTracking();
  appSession = session.fromPartition("persist:ptxttr", { cache: true });
  installStartupProtocol(appSession);
  installStartupActions();
  installPermissionPolicy(appSession);
  installMenu();
  installPowerPolicy();

  const firstWindow = createWindow({ startup: true });
  applyDevelopmentDockIcon();
  await restartSidecar(firstWindow);
}

function enableMacOSHistorySwipeTracking() {
  if (process.platform !== "darwin") return;
  // Chromium's native fluid history swiper consults these AppKit defaults.
  // Registering app-local defaults opts a fresh install in without overwriting
  // an explicit preference the user has already made for this bundle.
  systemPreferences.registerDefaults({
    AppleEnableSwipeNavigateWithScrolls: true,
    AppleEnableMouseSwipeNavigateWithScrolls: true,
  });
}

function sidecarPath() {
  if (process.env.PTXT_DESKTOP_SERVER_BINARY) {
    return path.resolve(process.env.PTXT_DESKTOP_SERVER_BINARY);
  }
  if (app.isPackaged) {
    return path.join(process.resourcesPath, "bin", "ptxt-nstr-server");
  }
  return path.resolve(desktopDir, "..", ".tmp", "desktop", "bin", "ptxt-nstr-server");
}

function logPath() {
  return path.join(app.getPath("logs"), "desktop.log");
}

function appendLog(message) {
  fs.mkdirSync(app.getPath("logs"), { recursive: true, mode: 0o700 });
  fs.appendFileSync(logPath(), `${new Date().toISOString()} ${message}\n`, { mode: 0o600 });
}

function dataDir() {
  return process.env.PTXT_DESKTOP_DATA_DIR
    ? path.resolve(process.env.PTXT_DESKTOP_DATA_DIR)
    : path.join(app.getPath("userData"), "local");
}

async function portAvailable() {
  return await new Promise((resolve) => {
    const probe = net.createServer();
    probe.unref();
    probe.once("error", () => resolve(false));
    probe.listen({ host: "127.0.0.1", port, exclusive: true }, () => {
      probe.close(() => resolve(true));
    });
  });
}

async function startSidecar() {
  if (!(await portAvailable())) {
    throw new Error(`Port ${port} is already in use.`);
  }
  const binary = sidecarPath();
  if (!fs.existsSync(binary)) {
    throw new Error(`Local server is missing: ${binary}`);
  }
  fs.mkdirSync(dataDir(), { recursive: true, mode: 0o700 });
  fs.mkdirSync(app.getPath("logs"), { recursive: true, mode: 0o700 });
  sidecarLogFD = fs.openSync(logPath(), "a", 0o600);
  sidecar = spawn(binary, [], {
    env: {
      ...process.env,
      PTXT_ADDR: `127.0.0.1:${port}`,
      PTXT_DESKTOP_DATA_DIR: dataDir(),
      PTXT_DESKTOP_MODE: "1",
      PTXT_DESKTOP_SESSION_TOKEN: activityToken,
    },
    stdio: ["ignore", sidecarLogFD, sidecarLogFD],
    windowsHide: true,
  });
  sidecar.once("error", (error) => appendLog(`sidecar spawn failed: ${error.message}`));
  sidecar.once("exit", (code, signal) => {
    appendLog(`sidecar exited code=${code ?? ""} signal=${signal ?? ""}`);
    sidecar = null;
    if (sidecarLogFD !== null) {
      fs.closeSync(sidecarLogFD);
      sidecarLogFD = null;
    }
  });
}

async function waitForReady(timeoutMS = 60_000) {
  const deadline = Date.now() + timeoutMS;
  while (Date.now() < deadline) {
    if (!sidecar || sidecar.exitCode !== null) throw new Error("The local server exited before becoming ready.");
    try {
      const response = await fetch(`${origin}/healthz`, { signal: AbortSignal.timeout(1_000) });
      if (response.ok) return;
    } catch {
      // The server may still be applying SQLite startup maintenance.
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error("The local server did not become ready within 60 seconds.");
}

async function restartSidecar(win = BrowserWindow.getFocusedWindow()) {
  if (restarting || quitting) return;
  restarting = true;
  try {
    await showStartup(win, "The local server is starting…");
    await stopSidecar();
    await startSidecar();
    await waitForReady();
		await installLoopbackSession();
    await setStartupActionsEnabled(win, false);
    await win?.loadURL(`${origin}/`);
    await updateActivity();
  } catch (error) {
    appendLog(`startup failed: ${error.stack || error.message}`);
    await showStartup(win, error.message || "The local server could not start.");
  } finally {
    restarting = false;
    while (pendingApplicationWindows > 0) {
      pendingApplicationWindows -= 1;
      requestApplicationWindow();
    }
  }
}

async function installLoopbackSession() {
	if (!appSession) throw new Error("The application session is unavailable.");
	await appSession.cookies.set({
		url: origin,
		name: "ptxt_desktop_token",
		value: activityToken,
		httpOnly: true,
		sameSite: "strict",
		path: "/",
	});
}

async function stopSidecar() {
  const child = sidecar;
  if (!child || child.exitCode !== null) return;
  await new Promise((resolve) => {
    let settled = false;
    let forceTimer;
    const finish = () => {
      if (settled) return;
      settled = true;
      clearTimeout(forceTimer);
      resolve();
    };
    child.once("exit", finish);
    child.kill("SIGTERM");
    forceTimer = setTimeout(() => {
      if (child.exitCode === null) child.kill("SIGKILL");
      finish();
    }, 5_000);
  });
}

function createWindow({ url = `${origin}/`, parent = null, background = false, startup = false } = {}) {
  const win = new BrowserWindow({
    width: 1120,
    height: 820,
    minWidth: 720,
    minHeight: 540,
    resizable: true,
    fullscreenable: true,
    show: !background,
    title: PRODUCT_NAME,
    ...(!app.isPackaged ? { icon: developmentIconPath() } : {}),
    tabbingIdentifier: TAB_GROUP_ID,
    ...macOSWindowChrome,
    webPreferences: {
      session: appSession,
      nodeIntegration: false,
      contextIsolation: true,
      sandbox: true,
      preload: path.join(desktopDir, "preload.cjs"),
      webSecurity: true,
      allowRunningInsecureContent: false,
      // macOS controls whether a two-finger page swipe is delivered as the
      // BrowserWindow swipe event; this restores the native rubber-band feel
      // once the system gesture is enabled.
      scrollBounce: process.platform === "darwin",
    },
  });
  windows.add(win);
  installWindowPolicy(win);
  if (parent && process.platform === "darwin") parent.addTabbedWindow(win);
  // The first window is loaded by restartSidecar so startup diagnostics and
  // the loopback navigation cannot race each other.
  if (!startup) void win.loadURL(url);
  if (!background) win.show();
  return win;
}

function requestApplicationWindow() {
  if (!appSession || restarting) {
    pendingApplicationWindows += 1;
    return null;
  }
  const needsStartup = !sidecar || sidecar.exitCode !== null;
  const win = createWindow({ startup: needsStartup });
  if (needsStartup) void showStartup(win, startupStatus);
  return win;
}

function installStartupProtocol(targetSession) {
  targetSession.protocol.handle("ptxt-startup", (request) => {
    const url = new URL(request.url);
    const asset = url.hostname === "app" ? startupAssets.get(url.pathname) : null;
    if (!asset) return new Response("Not found", { status: 404 });
    return new Response(asset.body, {
      headers: {
        "Cache-Control": "no-store",
        "Content-Type": asset.contentType,
      },
    });
  });
}

function installWindowPolicy(win) {
  const contents = win.webContents;
  let lastHistoryNavigationAt = 0;
  const navigateHistory = (action) => {
    const now = Date.now();
    // A native swipe event and the precise-wheel fallback can describe the
    // same physical gesture. Commit it once, not once through each channel.
    if (now - lastHistoryNavigationAt < 300) return "none";
    const result = action === "back"
      ? swipeNavigation("right", contents.navigationHistory)
      : swipeNavigation("left", contents.navigationHistory);
    if (result !== "none") lastHistoryNavigationAt = now;
    return result;
  };
  contents.on("did-navigate-in-page", () => {
    // Renderer wheel handling may commit the same physical gesture before
    // AppKit emits the legacy native swipe event.
    lastHistoryNavigationAt = Date.now();
  });
  contents.setWindowOpenHandler(({ url, disposition }) => {
    const target = classifyNavigation(url, origin);
    if (target.kind === "internal") {
      queueMicrotask(() => createWindow({
        url: target.url,
        parent: win,
        background: shouldOpenInBackground(disposition),
      }));
    } else if (target.kind === "external") {
      void shell.openExternal(target.url);
    }
    return { action: "deny" };
  });
  contents.on("will-navigate", (event, url) => {
    const target = classifyNavigation(url, origin);
    if (target.kind === "internal") return;
    event.preventDefault();
    if (target.kind === "external") void shell.openExternal(target.url);
    if (target.kind === "action") handleAction(target.action, win);
  });
  contents.on("context-menu", (_event, params) => showContextMenu(win, params));
  win.on("swipe", (_event, direction) => {
    if (direction === "right") navigateHistory("back");
    if (direction === "left") navigateHistory("forward");
  });
  win.on("new-window-for-tab", () => createWindow({ parent: win }));
  for (const event of ["show", "hide", "minimize", "restore", "closed"]) {
    win.on(event, () => void updateActivity());
  }
  win.on("closed", () => windows.delete(win));
}

function installStartupActions() {
  ipcMain.on("ptxt-startup-action", (event, action) => {
    if (event.senderFrame?.url !== "ptxt-startup://app/index.html") return;
    if (!new Set(["retry", "logs", "quit"]).has(action)) return;
    const win = BrowserWindow.fromWebContents(event.sender);
    if (win) handleAction(action, win);
  });
}

function showContextMenu(win, params) {
  const contents = win.webContents;
  const template = [];
  const selection = params.selectionText.trim();
  const link = params.linkURL ? classifyNavigation(params.linkURL, origin) : null;

  if (link?.kind === "internal") {
    template.push(
      {
        label: "Open Link in New Tab",
        click: () => createWindow({ url: link.url, parent: win }),
      },
      {
        label: "Open Link in New Background Tab",
        click: () => createWindow({ url: link.url, parent: win, background: true }),
      },
      {
        label: "Open Link",
        click: () => void contents.loadURL(link.url),
      },
      { type: "separator" },
      {
        label: "Copy Link",
        click: () => clipboard.writeText(link.url),
      },
    );
  } else if (link?.kind === "external") {
    template.push(
      { label: "Open Link in Browser", click: () => void shell.openExternal(link.url) },
      { type: "separator" },
      { label: "Copy Link", click: () => clipboard.writeText(link.url) },
    );
  }

  if (selection) {
    if (template.length) template.push({ type: "separator" });
    if (process.platform === "darwin") {
      const visibleSelection = selection.length > 60 ? `${selection.slice(0, 57)}…` : selection;
      template.push({
        label: `Look Up “${visibleSelection}”`,
        click: () => contents.showDefinitionForSelection(),
      });
    }
    template.push({ role: "copy" });
    if (process.platform === "darwin") template.push({ role: "services" });
  }

  if (params.isEditable) {
    if (template.length) template.push({ type: "separator" });
    if (params.misspelledWord) {
      for (const suggestion of params.dictionarySuggestions) {
        template.push({ label: suggestion, click: () => contents.replaceMisspelling(suggestion) });
      }
      if (params.dictionarySuggestions.length) template.push({ type: "separator" });
    }
    template.push(
      { role: "undo" },
      { role: "redo" },
      { type: "separator" },
      { role: "cut" },
      { role: "copy" },
      { role: "paste" },
      { role: "pasteAndMatchStyle" },
      { role: "selectAll" },
    );
  }

  if (!template.length) return;
  Menu.buildFromTemplate(template).popup({
    window: win,
    x: params.x,
    y: params.y,
    // Supplying the originating frame keeps macOS text services and writing
    // tools associated with the selected web content.
    frame: params.frame ?? undefined,
  });
}

async function showStartup(win, status) {
  if (!win || win.isDestroyed()) return;
  startupStatus = String(status);
  await win.loadURL("ptxt-startup://app/index.html");
  await win.webContents.executeJavaScript(
    `document.getElementById("status").textContent = ${JSON.stringify(startupStatus)}`,
    true,
  );
}

async function setStartupActionsEnabled(win, enabled) {
  if (!win || win.isDestroyed() || win.webContents.getURL() !== "ptxt-startup://app/index.html") return;
  await win.webContents.executeJavaScript(
    `for (const control of document.querySelectorAll("[data-ptxt-action]")) control.disabled = ${!enabled}`,
    true,
  );
}

function handleAction(action, win) {
  if (action === "retry") void restartSidecar(win);
  if (action === "logs") shell.showItemInFolder(logPath());
  if (action === "quit") app.quit();
}

function focusedHistory() {
  return BrowserWindow.getFocusedWindow()?.webContents.navigationHistory;
}

function installMenu() {
  const template = [
    { role: "appMenu" },
    {
      label: "File",
      submenu: [
        { label: "New Window", accelerator: "CmdOrCtrl+N", click: () => requestApplicationWindow() },
        { label: "New Tab", accelerator: "CmdOrCtrl+T", click: () => createWindow({ parent: BrowserWindow.getFocusedWindow() }) },
        { label: "Close Tab", accelerator: "CmdOrCtrl+W", click: () => BrowserWindow.getFocusedWindow()?.close() },
      ],
    },
    {
      label: "Edit",
      submenu: [
        { role: "undo" },
        { role: "redo" },
        { type: "separator" },
        { role: "cut" },
        { role: "copy" },
        { role: "paste" },
        { role: "pasteAndMatchStyle" },
        { role: "selectAll" },
        ...(process.platform === "darwin" ? [{ type: "separator" }, { role: "services" }] : []),
      ],
    },
    {
      label: "View",
      submenu: [
        { label: "Back", accelerator: "CmdOrCtrl+[", click: () => focusedHistory()?.canGoBack() && focusedHistory().goBack() },
        { label: "Forward", accelerator: "CmdOrCtrl+]", click: () => focusedHistory()?.canGoForward() && focusedHistory().goForward() },
        { label: "Reload", accelerator: "CmdOrCtrl+R", click: () => BrowserWindow.getFocusedWindow()?.webContents.reload() },
        { type: "separator" },
        { role: "togglefullscreen" },
      ],
    },
    { role: "windowMenu" },
    {
      role: "help",
      submenu: [{ label: "Open Logs", click: () => shell.showItemInFolder(logPath()) }],
    },
  ];
  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

function installPermissionPolicy() {
  const permitted = (webContents, permission, requestingOrigin) => {
    if (webContents?.session !== appSession) return false;
    if (requestingOrigin !== `${origin}/` && requestingOrigin !== origin) return false;
    return permission === "clipboard-sanitized-write";
  };
  appSession.setPermissionCheckHandler((webContents, permission, requestingOrigin) => (
    permitted(webContents, permission, requestingOrigin)
  ));
  appSession.setPermissionRequestHandler((webContents, permission, callback, details) => {
    callback(permitted(webContents, permission, details.requestingOrigin));
  });
}

function installPowerPolicy() {
  powerMonitor.on("suspend", () => {
    suspended = true;
    void updateActivity();
  });
  powerMonitor.on("resume", () => {
    suspended = false;
    void updateActivity();
  });
}

async function updateActivity() {
  if (!sidecar || sidecar.exitCode !== null) return;
  const visible = !suspended && [...windows].some((win) => (
    !win.isDestroyed() && win.isVisible() && !win.isMinimized()
  ));
  const mode = suspended ? "paused" : (visible ? "foreground" : "reduced");
  try {
    await fetch(`${origin}/__ptxt/desktop/activity`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Ptxt-Desktop-Token": activityToken,
      },
      body: JSON.stringify({ mode }),
      signal: AbortSignal.timeout(1_000),
    });
  } catch {
    // Startup and shutdown races are expected.
  }
}
