package adminui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Use the production outer middleware and exact registered mutation patterns:
// invalid CSRF must stop before any handler (and thus any provider RPC).
func TestContentRatingRoutesCSRF(t *testing.T) {
	for _, module := range []string{"media-movies", "media-tvshows"} {
		for _, tc := range []struct {
			name, cookie, header string
			code                 int
		}{
			{"missing", "", "", 403}, {"cookie-only", "fixture", "", 403},
			{"header-only", "", "fixture", 403}, {"mismatch", "fixture", "forged", 403},
			{"valid", "fixture", "fixture", 204},
		} {
			t.Run(module+"/"+tc.name, func(t *testing.T) {
				calls := 0
				mux := http.NewServeMux()
				mux.HandleFunc("POST /media/{moduleID}/item/{id}/content-rating", func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) })
				r := httptest.NewRequest("POST", "/media/"+module+"/item/fixture/content-rating", nil)
				if tc.cookie != "" {
					r.AddCookie(&http.Cookie{Name: "csrf-token", Value: tc.cookie})
				}
				if tc.header != "" {
					r.Header.Set("X-CSRF-Token", tc.header)
				}
				w := httptest.NewRecorder()
				withMiddleware(mux, nil, nil, "").ServeHTTP(w, r)
				if w.Code != tc.code || (calls == 1) != (tc.code == 204) {
					t.Fatalf("status=%d calls=%d", w.Code, calls)
				}
			})
		}
	}
}
