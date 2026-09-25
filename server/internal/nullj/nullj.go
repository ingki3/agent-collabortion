// Package nullj holds the `nullable.Nullable[T]` constructors every API mapper
// uses. They lived on `tasks` and still do (tasks.NullUUID delegates here), but
// the canonical copy is in a leaf package so a mapper can use them without
// taking a dependency on `tasks` — `messages` does, and `testdb`'s seed calls
// `messages.Store`, which closed a messages → tasks → testdb cycle in tasks'
// own test binary (review #335 NN5).
package nullj

import (
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func NullUUID(p *uuid.UUID) nullable.Nullable[openapi_types.UUID] {
	if p == nil {
		return nullable.NewNullNullable[openapi_types.UUID]()
	}
	return nullable.NewNullableWithValue(openapi_types.UUID(*p))
}

func NullTime(p *time.Time) nullable.Nullable[time.Time] {
	if p == nil {
		return nullable.NewNullNullable[time.Time]()
	}
	return nullable.NewNullableWithValue(*p)
}

func NullString(p *string) nullable.Nullable[string] {
	if p == nil {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(*p)
}
