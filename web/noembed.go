//go:build !embedweb

package web

import "io/fs"

// Dist is nil without -tags embedweb: in development Vite serves the app and proxies /api.
func Dist() fs.FS { return nil }
