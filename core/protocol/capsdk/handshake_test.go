package capsdk

import (
	"reflect"
	"testing"
)

// BUG-015 regression lock: HandshakeResponse.SessionToken is a bearer secret.
// The redact:"true" struct tag marks it for a future bus/log sanitiser to
// strip. Today the tag has no active consumer — it is a forward marker.
// This test pins the tag so a future field rename or tag drop is caught.
func TestHandshakeResponse_SessionTokenMarkedRedact(t *testing.T) {
	field, ok := reflect.TypeOf(HandshakeResponse{}).FieldByName("SessionToken")
	if !ok {
		t.Fatal("SessionToken field missing from HandshakeResponse")
	}
	if got := field.Tag.Get("redact"); got != "true" {
		t.Fatalf(`SessionToken redact tag = %q, want "true"`, got)
	}
}
