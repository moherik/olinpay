// Package apidocs embeds the canonical OpenAPI document into the OpenPay binary.
package apidocs

import "embed"

//go:embed openapi.yaml
var files embed.FS

// OpenAPI returns the canonical OpenAPI specification.
func OpenAPI() ([]byte, error) {
	return files.ReadFile("openapi.yaml")
}
