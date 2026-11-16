package updatepromptmux

import (
	"net/http/httptest"
	"testing"
)

// Given a request that no gorilla/mux route matched
// When its route is read
// Then it is empty, so the call counts as having no route
func TestPathTemplateWithoutARoute(t *testing.T) {
	request := httptest.NewRequest("GET", "http://api.example.com/.env", nil)

	if got := pathTemplate(request); got != "" {
		t.Errorf("Expected no route, got %q", got)
	}
}
