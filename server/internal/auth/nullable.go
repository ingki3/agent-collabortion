package auth

import (
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func nullableEmail(e string) nullable.Nullable[openapi_types.Email] {
	return nullable.NewNullableWithValue(openapi_types.Email(e))
}
