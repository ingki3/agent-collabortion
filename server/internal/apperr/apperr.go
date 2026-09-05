// Package apperr is the RFC 9457 problem type services return so the HTTP
// layer can write exact status/code pairs (openapi.md §1) without each
// package importing httpapi.
package apperr

import (
	"errors"
	"net/http"
)

type Problem struct {
	Status int
	Code   string
	Title  string
	Detail string
	Errors []FieldError
	Extra  map[string]any
}

type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func (p *Problem) Error() string { return p.Code + ": " + p.Detail }

func New(status int, code, detail string) *Problem {
	return &Problem{Status: status, Code: code, Title: http.StatusText(status), Detail: detail}
}

func Unauthorized(code, detail string) *Problem { return New(http.StatusUnauthorized, code, detail) }
func Forbidden(code, detail string) *Problem    { return New(http.StatusForbidden, code, detail) }
func NotFound(what string) *Problem             { return New(http.StatusNotFound, "not_found", what+" not found") }
func Conflict(code, detail string) *Problem     { return New(http.StatusConflict, code, detail) }
func Gone(code, detail string) *Problem         { return New(http.StatusGone, code, detail) }
func Validation(errs ...FieldError) *Problem {
	p := New(http.StatusUnprocessableEntity, "validation_failed", "Validation failed")
	p.Errors = errs
	return p
}
func Field(field, code, msg string) FieldError {
	return FieldError{Field: field, Code: code, Message: msg}
}
func Internal(err error) *Problem {
	return New(http.StatusInternalServerError, "internal", err.Error())
}

// As converts any error to a Problem (unknown errors become 500).
func As(err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return Internal(err)
}
