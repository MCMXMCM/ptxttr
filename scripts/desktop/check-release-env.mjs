const required = [
  "APPLE_SIGNING_IDENTITY",
  "APPLE_ID",
  "APPLE_APP_SPECIFIC_PASSWORD",
  "APPLE_TEAM_ID",
];
const missing = required.filter((name) => !String(process.env[name] || "").trim());
if (missing.length) {
  console.error(`Missing release environment: ${missing.join(", ")}`);
  process.exit(1);
}
