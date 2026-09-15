// Package gen holds the types and the std-http router generated from
// contracts/openapi.yaml by oapi-codegen. Do not edit api.gen.go by hand —
// change the contract (Director-approved PR) and regenerate.
//
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config config.yaml ../../../../contracts/openapi.yaml
package gen
