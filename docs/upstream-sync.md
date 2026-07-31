# Upstream synchronization boundary

The end-user web application is pinned to the private `ptxt-nstr` revision recorded in `upstream-sync.json`. The target baseline records the public repository revision from which this clean-break Electron work began.

`npm run check:upstream` compares every allowlisted, non-overlay application file byte-for-byte with the pinned source. A deliberate local-first difference must be named in `localOverlays`; adding a broad new overlay is a review event. Generated web bundles remain overlays because local capability and storage modules change their content hashes.

Synchronization workflow:

1. Check out the desired private source revision and update `sourceRevision` only after reviewing its complete diff.
2. Copy user-facing server, route, template, CSS, JavaScript, media, mutation, and test changes covered by `syncAllowlist`.
3. Keep desktop runtime, capability, activity, storage, and resource-policy changes in their explicit overlays.
4. Run the complete Go, browser unit, route E2E, WebKit fallback, and Electron suites.
5. Run `npm run check:upstream` and `npm run check:public-tree` before packaging, then run the same public-tree scan against the unpacked application/DMG payload.

The denylist excludes hosted deployment and operator material, CloudFormation/CloudFront assets, environment files, databases, logs, profiles, archives, patches, production snapshots, proprietary fonts, private plans/audits, credentials, account identifiers/ARNs, private keys, personal paths, and signing identities. Signing and notarization values are supplied only by the environment or CI secret store.

Environment-specific exceptions are intentional: browser NIP-07 signing remains a hosted/browser capability, while hosted administration and infrastructure never enter this repository. Ordinary reader, writer, media, account, relay, navigation, and local-first behavior remains in synchronization scope.
