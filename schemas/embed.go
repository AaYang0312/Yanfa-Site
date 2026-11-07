// Package schemas embeds the versioned source contracts used by Go and TypeScript.
package schemas

import "embed"

// Files contains the immutable source contracts; no network schema loading is needed.
//
//go:embed *.schema.json http-api.openapi.yaml
var Files embed.FS
