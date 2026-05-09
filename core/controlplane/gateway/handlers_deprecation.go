package gateway

import "net/http"

func markDeprecatedEndpoint(w http.ResponseWriter, successor string) {
	w.Header().Set("Deprecation", "true")
	w.Header().Add("Link", "<"+successor+">; rel=\"successor-version\"")
}
