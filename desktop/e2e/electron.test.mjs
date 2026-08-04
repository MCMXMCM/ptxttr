import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { _electron as electron } from "playwright";

const root = path.resolve(import.meta.dirname, "..", "..");
const origin = "http://127.0.0.1:24787";

async function launch(userData, extraEnv = {}) {
  return electron.launch({
    args: [root],
	executablePath: path.join(root, "node_modules", "electron", "dist", "Electron.app", "Contents", "MacOS", "Electron"),
    env: {
      ...process.env,
      PTXT_ELECTRON_USER_DATA: userData,
      PTXT_DESKTOP_SERVER_BINARY: path.join(root, ".tmp", "desktop", "bin", "ptxt-nstr-server"),
      PTXT_REQUEST_TIMEOUT_MS: "1000",
      ...extraEnv,
    },
  });
}

async function step(label, promise, timeout = 20_000) {
	let timer;
	try {
		return await Promise.race([
			promise,
			new Promise((_, reject) => {
				timer = setTimeout(() => reject(new Error(`timed out: ${label}`)), timeout);
			}),
		]);
	} finally {
		clearTimeout(timer);
	}
}

async function closeElectron(electronApp) {
  await step("Electron shutdown", electronApp.close(), 20_000);
}

function removeTree(target) {
  fs.rmSync(target, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
}

async function portIsAvailable() {
  const probe = net.createServer();
  return new Promise((resolve) => {
    probe.once("error", () => resolve(false));
    probe.listen(24787, "127.0.0.1", () => probe.close(() => resolve(true)));
  });
}

async function measureWideTreeLayout(page) {
  return page.evaluate(() => {
    const feed = document.querySelector(".feed-column");
    const participants = document.querySelector(".right-rail");
    const priorFragment = participants?.dataset.threadFragment;
    if (participants) participants.dataset.threadFragment = "participants";
    document.body.classList.add("thread-tree-wide-layout");
    const box = feed?.getBoundingClientRect();
    const result = {
      left: box?.left || 0,
      right: box?.right || 0,
      viewportWidth: window.innerWidth,
      width: box?.width || 0,
    };
    document.body.classList.remove("thread-tree-wide-layout");
    if (participants) {
      if (priorFragment === undefined) delete participants.dataset.threadFragment;
      else participants.dataset.threadFragment = priorFragment;
    }
    return result;
  });
}

test("desktop starts one sandboxed sidecar and keeps native tabs session-shared", { timeout: 90_000 }, async (t) => {
  const userData = fs.mkdtempSync(path.join(os.tmpdir(), "ptxttr-electron-"));
  t.after(() => removeTree(userData));
	const electronApp = await step("Electron launch", launch(userData), 30_000);
	t.after(async () => closeElectron(electronApp).catch(() => {}));
	const home = await step("first window", electronApp.firstWindow());
	await step("home readiness", home.waitForURL(`${origin}/`), 70_000);
  const sidebarToggle = home.locator("[data-sidebar-collapse-toggle]");
  await sidebarToggle.waitFor({ state: "visible" });
  const expandedSidebar = await home.locator(".left-rail").boundingBox();
  assert.ok(expandedSidebar.x <= 1);
  assert.equal(expandedSidebar.width, 224);
  const wideTreeLayout = await measureWideTreeLayout(home);
  assert.ok(wideTreeLayout.width > 800);
  assert.ok(wideTreeLayout.right >= wideTreeLayout.viewportWidth - 1);
  await sidebarToggle.click();
  await home.waitForFunction(() => document.documentElement.dataset.ptxtSidebarCollapsed === "1");
  assert.equal(await sidebarToggle.getAttribute("aria-expanded"), "false");
  assert.equal(await home.evaluate(() => localStorage.getItem("ptxt_desktop_sidebar_collapsed")), "1");
  assert.equal(await home.locator(".left-rail").isVisible(), false);
  const collapsedWideTreeLayout = await measureWideTreeLayout(home);
  assert.ok(collapsedWideTreeLayout.left <= 1);
  assert.ok(collapsedWideTreeLayout.right >= collapsedWideTreeLayout.viewportWidth - 1);
  const titlebarToggle = await sidebarToggle.boundingBox();
  assert.equal(titlebarToggle.x, 88);
  assert.equal(titlebarToggle.y, 5);
  await electronApp.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].setSize(800, 700));
  await home.waitForTimeout(100);
  assert.equal(await home.locator(".left-rail").isVisible(), false);
  assert.equal(await sidebarToggle.isVisible(), true);
  await sidebarToggle.click();
  await home.waitForFunction(() => !document.documentElement.dataset.ptxtSidebarCollapsed);
  assert.equal(await sidebarToggle.getAttribute("aria-expanded"), "true");
  await electronApp.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].setSize(1120, 820));
  assert.equal(await home.locator("[data-desktop-window-drag-region]").evaluate(
    (node) => getComputedStyle(node).getPropertyValue("-webkit-app-region"),
  ), "drag");
  assert.equal(await electronApp.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].isResizable()), true);
  assert.equal(await home.evaluate(() => typeof globalThis.require), "undefined");
  assert.equal(await home.evaluate(() => typeof globalThis.process), "undefined");
  await home.evaluate(() => localStorage.setItem("electron-session-probe", "shared"));

  await electronApp.evaluate(({ Menu }) => {
    globalThis.__ptxtContextMenu = null;
    Menu.prototype.popup = function popup(options) {
      globalThis.__ptxtContextMenu = {
        labels: this.items.map((item) => item.label).filter(Boolean),
        hasFrame: Boolean(options.frame),
      };
    };
  });
  await home.evaluate(() => {
    const link = document.createElement("a");
    link.id = "desktop-context-link";
    link.href = "/settings";
    link.textContent = "Desktop context link";
    document.body.append(link);
  });
  await home.locator("#desktop-context-link").click({ button: "right" });
  await step("link context menu", home.waitForTimeout(100));
  const contextMenu = await electronApp.evaluate(() => globalThis.__ptxtContextMenu);
  assert.equal(contextMenu.hasFrame, true);
  assert.deepEqual(contextMenu.labels.slice(0, 4), [
    "Open Link in New Tab",
    "Open Link in New Background Tab",
    "Open Link",
    "Copy Link",
  ]);
  assert.ok(contextMenu.labels.includes("Copy"));
  assert.ok(contextMenu.labels.some((label) => label.startsWith("Look Up “")));
  assert.ok(contextMenu.labels.includes("Services"));

	await electronApp.evaluate(({ Menu }) => {
		const fileMenu = Menu.getApplicationMenu().items.find((item) => item.label === "File");
		fileMenu.submenu.items.find((item) => item.label === "New Tab").click();
	});
	await step("new native tab", home.waitForTimeout(500));
	assert.equal(await electronApp.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().length), 2);
	assert.equal(electronApp.windows().length, 2);
  const second = electronApp.windows().find((page) => page !== home);
	await step("new tab home", second.waitForURL(`${origin}/`));
  assert.equal(await second.evaluate(() => localStorage.getItem("electron-session-probe")), "shared");
  await second.goto(`${origin}/settings`);
  assert.equal(new URL(home.url()).pathname, "/");

  await second.bringToFront();
  await electronApp.evaluate(({ BrowserWindow }) => {
    const settingsWindow = BrowserWindow.getAllWindows().find(
      (window) => new URL(window.webContents.getURL()).pathname === "/settings",
    );
    settingsWindow.webContents.sendInputEvent({
      type: "mouseWheel",
      hasPreciseScrollingDeltas: true,
      deltaX: 40,
      deltaY: 0,
      x: 500,
      y: 400,
    });
    settingsWindow.webContents.sendInputEvent({
      type: "mouseWheel",
      hasPreciseScrollingDeltas: true,
      deltaX: 50,
      deltaY: 0,
      x: 500,
      y: 400,
    });
  });
  await step("two-finger back navigation", second.waitForURL(`${origin}/`));

	await second.bringToFront();
	const closed = second.waitForEvent("close");
	await electronApp.evaluate(({ Menu }) => {
		const fileMenu = Menu.getApplicationMenu().items.find((item) => item.label === "File");
		fileMenu.submenu.items.find((item) => item.label === "Close Tab").click();
	});
	await closed;
  assert.equal(electronApp.windows().length, 1);

  const secondLaunchWindow = electronApp.waitForEvent("window");
  await electronApp.evaluate(({ app }) => app.emit("second-instance", {}, [], ""));
  const launchedAgain = await step("second launch window", secondLaunchWindow);
  await step("second launch home", launchedAgain.waitForURL(`${origin}/`));
  assert.equal(electronApp.windows().length, 2);
  assert.equal(await launchedAgain.evaluate(() => localStorage.getItem("electron-session-probe")), "shared");
  assert.ok(await electronApp.evaluate(({ Menu }) => {
    const fileMenu = Menu.getApplicationMenu().items.find((item) => item.label === "File");
    return fileMenu.submenu.items.some((item) => item.label === "New Window");
  }));
  await launchedAgain.close();
  assert.equal(electronApp.windows().length, 1);
	await closeElectron(electronApp);
  assert.equal(fs.existsSync(path.join(userData, "local", "ptxt-nstr.sqlite")), true);
  assert.equal(await portIsAvailable(), true);
});

test("port collision stays on the diagnostic startup screen", { timeout: 30_000 }, async (t) => {
  const blocker = net.createServer();
  await new Promise((resolve, reject) => blocker.once("error", reject).listen(24787, "127.0.0.1", resolve));
  t.after(() => blocker.close());
  const userData = fs.mkdtempSync(path.join(os.tmpdir(), "ptxttr-collision-"));
  t.after(() => removeTree(userData));
	const electronApp = await step("collision Electron launch", launch(userData), 30_000);
	t.after(async () => closeElectron(electronApp).catch(() => {}));
  const page = await electronApp.firstWindow();
  const startupDetails = page.locator("[data-startup-details]");
  await startupDetails.waitFor({ state: "attached" });
  assert.deepEqual(await startupDetails.evaluate((details) => {
    const [reveal] = details.getAnimations();
    reveal.pause();
    reveal.currentTime = 0;
    const style = getComputedStyle(details);
    return {
      delay: reveal.effect.getTiming().delay,
      visibility: style.visibility,
      maxHeight: style.maxHeight,
      paddingTop: style.paddingTop,
    };
  }), {
    delay: 3_000,
    visibility: "hidden",
    maxHeight: "0px",
    paddingTop: "0px",
  });
  await page.waitForFunction(
    () => document.getElementById("status")?.textContent.includes("Port 24787 is already in use."),
  );
  await startupDetails.evaluate((details) => {
    const [reveal] = details.getAnimations();
    reveal.currentTime = reveal.effect.getComputedTiming().endTime;
  });
  await page.waitForSelector("text=Port 24787 is already in use.");
  assert.equal(await startupDetails.evaluate((details) => getComputedStyle(details).visibility), "visible");
  const lightIcon = page.locator('[data-startup-icon="light"]');
  const darkIcon = page.locator('[data-startup-icon="dark"]');
  await lightIcon.waitFor({ state: "attached" });
  assert.equal(await lightIcon.evaluate((icon) => icon.naturalWidth), 1024);
  assert.equal(await darkIcon.evaluate((icon) => icon.naturalWidth), 1024);
  const lightSource = await lightIcon.getAttribute("src");
  const darkSource = await darkIcon.getAttribute("src");
  assert.equal(lightSource, "/icon-light.png");
  assert.equal(darkSource, "/icon-dark.png");
  assert.notEqual(lightSource, darkSource);
  await page.emulateMedia({ colorScheme: "dark" });
  assert.equal(await lightIcon.evaluate((icon) => getComputedStyle(icon).display), "none");
  assert.equal(await darkIcon.evaluate((icon) => getComputedStyle(icon).display), "block");
  assert.match(await page.textContent("body"), /Retry.*Open Logs.*Quit/s);
});

test("missing sidecar stays on a diagnostic screen", { timeout: 20_000 }, async (t) => {
  const userData = fs.mkdtempSync(path.join(os.tmpdir(), "ptxttr-missing-sidecar-"));
  t.after(() => removeTree(userData));
  const electronApp = await step(
    "missing-sidecar Electron launch",
    launch(userData, { PTXT_DESKTOP_SERVER_BINARY: path.join(userData, "missing-server") }),
    30_000,
  );
  t.after(async () => closeElectron(electronApp).catch(() => {}));
  const page = await electronApp.firstWindow();
  await page.waitForSelector("text=Local server is missing:");
  assert.match(await page.textContent("body"), /Retry.*Open Logs.*Quit/s);
});

test("restart reuses fresh Electron state without importing an older app directory", { timeout: 90_000 }, async (t) => {
  const userData = fs.mkdtempSync(path.join(os.tmpdir(), "ptxttr-persist-"));
  t.after(() => removeTree(userData));
	let electronApp = await step("first persistence Electron launch", launch(userData), 30_000);
	let page = await step("first persistence window", electronApp.firstWindow());
	await step("first persistence readiness", page.waitForURL(`${origin}/`), 70_000);
  await page.evaluate(() => localStorage.setItem("restart-probe", "present"));
	await closeElectron(electronApp);

	electronApp = await step("second persistence Electron launch", launch(userData), 30_000);
	t.after(async () => closeElectron(electronApp).catch(() => {}));
	page = await step("second persistence window", electronApp.firstWindow());
	await step("second persistence readiness", page.waitForURL(`${origin}/`), 70_000);
  assert.equal(await page.evaluate(() => localStorage.getItem("restart-probe")), "present");
});
