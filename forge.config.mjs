import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { FusesPlugin } from "@electron-forge/plugin-fuses";
import { FuseV1Options, FuseVersion } from "@electron/fuses";

const root = path.dirname(fileURLToPath(import.meta.url));
const hasSigningIdentity = Boolean(process.env.APPLE_SIGNING_IDENTITY);
const hasNotaryCredentials = Boolean(
  process.env.APPLE_ID && process.env.APPLE_APP_SPECIFIC_PASSWORD && process.env.APPLE_TEAM_ID,
);

export default {
  packagerConfig: {
    name: "Plain Text Nostr",
    executableName: "Plain Text Nostr",
    appBundleId: "com.ptxttr.desktop",
    osxMinVersion: "12.0",
    icon: path.join(root, "desktop", "icon.icns"),
    extendInfo: {
      CFBundleIconFile: "PlainTextNostr.icns",
    },
    asar: true,
    ignore: (file) => {
      const candidate = String(file || "");
      const relative = candidate.startsWith(`${root}${path.sep}`) ? path.relative(root, candidate) : candidate;
      const normalized = relative.replaceAll("\\\\", "/").replace(/^\//, "");
      if (!normalized) return false;
      if (normalized === "desktop/entitlements.plist" || normalized.endsWith(".test.mjs") || normalized.startsWith("desktop/e2e")) return true;
      const startupIconPath = normalized === "web"
        || normalized === "web/static"
        || normalized === "web/static/img"
        || normalized === "web/static/img/ascritch_icon_black.png"
        || normalized === "web/static/img/ascritch_icon_white.png";
      return normalized !== "package.json" && normalized !== "desktop" && !normalized.startsWith("desktop/") && !startupIconPath;
    },
    extraResource: [path.join(root, ".tmp", "desktop", "bin")],
    ...(hasSigningIdentity ? {
      osxSign: {
        identity: process.env.APPLE_SIGNING_IDENTITY,
        optionsForFile: () => ({ entitlements: path.join(root, "desktop", "entitlements.plist") }),
      },
    } : {}),
    ...(hasNotaryCredentials ? {
      osxNotarize: {
        appleId: process.env.APPLE_ID,
        appleIdPassword: process.env.APPLE_APP_SPECIFIC_PASSWORD,
        teamId: process.env.APPLE_TEAM_ID,
      },
    } : {}),
  },
  rebuildConfig: {},
  makers: [
    { name: "@electron-forge/maker-dmg", config: { format: "ULFO" } },
    { name: "@electron-forge/maker-zip", platforms: ["darwin"] },
  ],
  hooks: {
    postPackage: async (_forgeConfig, { platform, outputPaths }) => {
      if (platform !== "darwin" || hasSigningIdentity) return;
      for (const outputPath of outputPaths) {
        const appPath = path.join(outputPath, "Plain Text Nostr.app");
        execFileSync("codesign", ["--sign", "-", "--force", "--deep", appPath], { stdio: "inherit" });
      }
    },
  },
  plugins: [
    new FusesPlugin({
      version: FuseVersion.V1,
      [FuseV1Options.RunAsNode]: false,
      [FuseV1Options.EnableCookieEncryption]: true,
      [FuseV1Options.EnableNodeOptionsEnvironmentVariable]: false,
      [FuseV1Options.EnableNodeCliInspectArguments]: false,
      [FuseV1Options.EnableEmbeddedAsarIntegrityValidation]: true,
      [FuseV1Options.OnlyLoadAppFromAsar]: true,
      [FuseV1Options.LoadBrowserProcessSpecificV8Snapshot]: false,
      [FuseV1Options.GrantFileProtocolExtraPrivileges]: false,
      [FuseV1Options.WasmTrapHandlers]: true,
      strictlyRequireAllFuses: true,
      resetAdHocDarwinSignature: false,
    }),
  ],
};
