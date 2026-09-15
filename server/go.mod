module github.com/ingki3/agent-collabortion/server

go 1.25.0

require (
	github.com/ingki3/agent-collabortion/contracts v0.0.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/oapi-codegen/nullable v1.2.0
	github.com/oapi-codegen/runtime v1.7.0
)

require golang.org/x/sys v0.39.0 // indirect

require (
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/google/uuid v1.6.0
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.46.0
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.32.0 // indirect
)

replace github.com/ingki3/agent-collabortion/contracts => ../contracts
