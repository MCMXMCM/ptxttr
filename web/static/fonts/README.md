# Local font assets

Font binaries are intentionally not distributed with the public source tree.
The application builds without them and falls back to system monospace fonts.
Authorized local builds may provide `local-mono-variable.woff2` in this
directory; `.gitignore` keeps it out of source control while the asset embed
includes it in locally built binaries.
