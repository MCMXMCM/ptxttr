package templatesfs

import "embed"

// FS contains server HTML templates.
//
//go:embed *.html
var FS embed.FS
