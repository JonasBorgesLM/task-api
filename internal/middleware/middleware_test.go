package middleware

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestChain_ExecutesInDeclaredOrder(t *testing.T) {
	var order []string

	tag := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+":before")
				next.ServeHTTP(w, r)
				order = append(order, name+":after")
			})
		}
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	})

	chained := Chain(tag("a"), tag("b"), tag("c"))(handler)
	chained.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"a:before", "b:before", "c:before", "handler", "c:after", "b:after", "a:after"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("Chain() execution order = %v, want %v", order, want)
	}
}

func TestChain_NoMiddlewares_ReturnsHandlerUnchanged(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	chained := Chain()(handler)

	w := httptest.NewRecorder()
	chained.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusTeapot {
		t.Errorf("Chain() with no middlewares status = %d, want %d", w.Code, http.StatusTeapot)
	}
}
