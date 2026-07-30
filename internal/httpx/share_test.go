package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestCreateShareAllowsAnonymous(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	ev := signedMutationEvent(t, nostrx.KindTextNote, "anonymous share note", nil)
	if err := st.SaveEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}

	raw := `{"note_id":"` + ev.ID + `","surface":"note_op"}`
	req := httptest.NewRequest(http.MethodPost, "/api/shares", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.handleCreateShare(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var out createShareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" {
		t.Fatal("expected share token for anonymous request")
	}
	if !strings.Contains(out.URL, "/s/"+out.Token) {
		t.Fatalf("url = %q, want tokenized /s/ path", out.URL)
	}
}

func TestCreateShareMintAndRenderTextShare(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	root := signedMutationEvent(t, nostrx.KindTextNote, "root share note", nil)
	reply := signedMutationEvent(t, nostrx.KindTextNote, "reply share note", [][]string{
		{"e", root.ID, "", "root"},
		{"e", root.ID, "", "reply"},
	})
	if err := st.SaveEvent(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, reply); err != nil {
		t.Fatal(err)
	}

	body := map[string]string{
		"note_id":   reply.ID,
		"surface":   shareSurfaceThreadFocus,
		"root_id":   root.ID,
		"parent_id": root.ID,
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/shares", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ptxt-Viewer", strings.Repeat("a", 64))
	rec := httptest.NewRecorder()

	srv.handleCreateShare(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var out createShareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" {
		t.Fatal("expected share token")
	}
	if got, want := out.LiveThreadURL, "/thread/"+root.ID+"?selected="+reply.ID+"#note-"+reply.ID; got != want {
		t.Fatalf("live_thread_url = %q, want %q", got, want)
	}
	if !strings.Contains(out.URL, "/s/"+out.Token) {
		t.Fatalf("url = %q, want tokenized /s/ path", out.URL)
	}
	if !strings.Contains(out.OGImageURL, "/og/share/"+out.Token+".png") {
		t.Fatalf("og_image_url = %q, want text-share og image", out.OGImageURL)
	}

	shareReq := httptest.NewRequest(http.MethodGet, "/s/"+out.Token, nil)
	shareRec := httptest.NewRecorder()
	srv.handleSharePage(shareRec, shareReq)
	if shareRec.Code != http.StatusOK {
		t.Fatalf("share page status = %d, want 200 body=%s", shareRec.Code, shareRec.Body.String())
	}
	bodyText := shareRec.Body.String()
	if !strings.Contains(bodyText, "View thread") {
		t.Fatalf("share page missing live thread link: %s", bodyText)
	}
	if !strings.Contains(bodyText, out.LiveThreadURL) {
		t.Fatalf("share page missing live thread href %q", out.LiveThreadURL)
	}
	if !strings.Contains(bodyText, `/og/share/`+out.Token+`.png`) {
		t.Fatalf("share page missing text og image token path: %s", bodyText)
	}

	ogReq := httptest.NewRequest(http.MethodGet, "/og/share/"+out.Token+".png", nil)
	ogRec := httptest.NewRecorder()
	srv.handleShareOGImage(ogRec, ogReq)
	if ogRec.Code != http.StatusOK {
		t.Fatalf("share og status = %d, want 200", ogRec.Code)
	}
	if got := ogRec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content-type = %q, want image/png", got)
	}
}

func TestCreateShareUsesDirectImageForImageNote(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	ev := signedMutationEvent(t, nostrx.KindTextNote, "https://cdn.example.com/share.png", nil)
	if err := st.SaveEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}

	raw := `{"note_id":"` + ev.ID + `","surface":"note_op"}`
	req := httptest.NewRequest(http.MethodPost, "/api/shares", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ptxt-Viewer", strings.Repeat("b", 64))
	rec := httptest.NewRecorder()
	srv.handleCreateShare(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var out createShareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.HasImage {
		t.Fatal("expected has_image=true")
	}
	if got, want := out.OGImageURL, "https://cdn.example.com/share.png"; got != want {
		t.Fatalf("og_image_url = %q, want %q", got, want)
	}

	shareReq := httptest.NewRequest(http.MethodGet, "/s/"+out.Token, nil)
	shareRec := httptest.NewRecorder()
	srv.handleSharePage(shareRec, shareReq)
	if shareRec.Code != http.StatusOK {
		t.Fatalf("share page status = %d, want 200", shareRec.Code)
	}
	if !strings.Contains(shareRec.Body.String(), `content="https://cdn.example.com/share.png"`) {
		t.Fatalf("share page should expose direct og:image: %s", shareRec.Body.String())
	}

	ogReq := httptest.NewRequest(http.MethodGet, "/og/share/"+out.Token+".png", nil)
	ogRec := httptest.NewRecorder()
	srv.handleShareOGImage(ogRec, ogReq)
	if ogRec.Code != http.StatusNotFound {
		t.Fatalf("image share og route status = %d, want 404", ogRec.Code)
	}
}
