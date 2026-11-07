package httpapi

import (
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/content"
	management "yuanfa-official/backend/internal/mcp"
	"yuanfa-official/backend/internal/media"
	"yuanfa-official/backend/internal/publication"
)

// RegisterMCP applies Bearer/Origin checks to every HTTP request, including SDK
// initialize, list, GET and later calls on a previously initialized client. The
// SDK owns protocol negotiation and method handling (stateless GET/DELETE=405).
// Tools additionally reauthenticate their request headers at invocation time.
func RegisterMCP(mux *http.ServeMux, m AuthMiddleware, products *content.Service, images *media.Service, publishing *publication.Service) error {
	server, err := management.New(m.Auth, products, images, publishing)
	if err != nil {
		return err
	}
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true})
	mux.Handle("/mcp", m.Bearer(func(w http.ResponseWriter, r *http.Request, _ auth.Actor) {
		r.Body = http.MaxBytesReader(w, r.Body, MaxAdminJSON)
		transport.ServeHTTP(w, r)
	}))
	return nil
}
