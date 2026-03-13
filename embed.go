// Package agenthub provides the embedded web assets for the agenthub binary.
// This file must remain at the module root (same directory as web/) because
// Go's //go:embed directive cannot use ".." path components.
package agenthub

import "embed"

//go:embed web/templates
var Templates embed.FS

//go:embed web/static
var Static embed.FS
