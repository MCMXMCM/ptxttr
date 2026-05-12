# macOS desktop — maintainer notes

This document is for **maintainers** who codesign, notarize, package, and publish the macOS Wails app. Visitors and casual contributors should use [the root README](../../README.md) (clone + `make build-desktop`, or download the DMG from GitHub Releases).

## Build

From the repo root on a Mac (Wails CLI + Xcode Command Line Tools installed):

```sh
make build-desktop
# equivalent: make desktop-build
```

`scripts/desktop/build-mac.sh` runs `wails build` with `-skipbindings` because the shell does not expose Go methods to the embedded splash; the UI is the loopback `httpx` app only.

Unsigned output: `cmd/desktop/build/bin/ptxt-nstr.app`.

## Sign and notarize (Developer ID)

`scripts/desktop/sign-mac.sh` is invoked by `make desktop-sign`. It codesigns, notarizes with Apple, and staples the ticket. Copy `scripts/desktop/signing.env.example` to `scripts/desktop/signing.env` (gitignored), fill in secrets, or export the variables documented in the header of `sign-mac.sh`.

When `DEVELOPER_ID_APPLICATION` is unset, signing is effectively skipped so local dev builds still work.

Typical release order:

```sh
make desktop-build
make desktop-sign
make desktop-package
```

Run **package after sign** so the DMG contains a signed bundle (`desktop-package` does not rebuild).

## DMG and versioning

`make desktop-package` runs `scripts/desktop/package-mac.sh` and writes **`dist/ptxt-nstr-desktop-mac-<version>.dmg`**.

Set the version string via `PTXT_NSTR_DESKTOP` (or in `signing.env`), or bump `productVersion` in `cmd/desktop/wails.json`.

## GitHub Releases

Create a release from a tag (GitHub **Releases → Draft** or `gh release create v0.1.0 --generate-notes`), then attach:

- the DMG from `dist/*.dmg` after `make desktop-package`, or
- a zip of the `.app` built with `ditto -c -k --sequesterRsrc --keepParent` from `cmd/desktop/build/bin/` — **do not** use plain `zip` on the bundle; it can break code signatures.

Do not commit signed binaries or DMGs to git; users download assets from the release page.
