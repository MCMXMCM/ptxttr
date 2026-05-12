package httpx

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"ptxt-nstr/internal/config"
)

func TestHandleDesktopOpenExternalNotRegisteredOff(t *testing.T) {
	cfg := config.Config{DesktopMode: false}
	s := &Server{cfg: cfg}
	req := httptest.NewRequest(http.MethodPost, desktopOpenExternalPath, bytes.NewReader([]byte(`{"url":"https://example.com/"}`)))
	rec := httptest.NewRecorder()
	s.handleDesktopOpenExternal(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d want 404", rec.Code)
	}
}

func TestHandleDesktopOpenExternalRejectsNonHTTP(t *testing.T) {
	cfg := config.Config{DesktopMode: true}
	s := &Server{cfg: cfg}
	req := httptest.NewRequest(http.MethodPost, desktopOpenExternalPath, bytes.NewReader([]byte(`{"url":"javascript:alert(1)"}`)))
	rec := httptest.NewRecorder()
	s.handleDesktopOpenExternal(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
}
