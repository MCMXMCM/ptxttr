package staticfs

import "embed"

// FS contains embedded static web assets.
//
//go:embed css/*.css js/*.js lib/*.js img/*.png img/*.ico
var FS embed.FS
