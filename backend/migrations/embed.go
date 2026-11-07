// Package migrations embeds the ordered SQL history shipped with the binary.
// Never modify an applied migration; add the next contiguous numbered file.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
