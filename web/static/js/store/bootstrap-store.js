import {
  openClientDB,
  requestResult,
  STORE_METADATA,
  transactionDone,
} from "../client-store.js";

const APP_BOOTSTRAP_KEY = "app_bootstrap";
const ROUTE_VIEWMODEL_PREFIX = "route_viewmodel:";

async function putMetadataRecord(record) {
  const db = await openClientDB();
  const tx = db.transaction(STORE_METADATA, "readwrite");
  tx.objectStore(STORE_METADATA).put(record);
  await transactionDone(tx);
  return record;
}

async function getMetadataRecord(key) {
  const db = await openClientDB();
  const tx = db.transaction(STORE_METADATA, "readonly");
  const result = await requestResult(tx.objectStore(STORE_METADATA).get(String(key || "")));
  await transactionDone(tx);
  return result || null;
}

export async function saveAppBootstrapSnapshot(bootstrap) {
  if (!bootstrap || typeof bootstrap !== "object") return null;
  return putMetadataRecord({
    key: APP_BOOTSTRAP_KEY,
    kind: "app_bootstrap",
    updated_at: Date.now(),
    payload: bootstrap,
  });
}

export async function readAppBootstrapSnapshot() {
  const row = await getMetadataRecord(APP_BOOTSTRAP_KEY);
  return row?.payload || null;
}

export async function saveRouteViewModelSnapshot(route, url, viewModel) {
  if (!route || !url || !viewModel) return null;
  return putMetadataRecord({
    key: `${ROUTE_VIEWMODEL_PREFIX}${route}:${url}`,
    kind: "route_viewmodel",
    updated_at: Date.now(),
    payload: {
      route,
      url,
      viewModel,
    },
  });
}

export async function readRouteViewModelSnapshot(route, url) {
  if (!route || !url) return null;
  const row = await getMetadataRecord(`${ROUTE_VIEWMODEL_PREFIX}${route}:${url}`);
  return row?.payload || null;
}
