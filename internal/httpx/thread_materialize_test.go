package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

func TestThreadProjectionStatusRequiresCompleteSelectedPath(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	root := signedMutationEvent(t, nostrx.KindTextNote, "projection root", nil)
	parent := signedMutationEvent(t, nostrx.KindTextNote, "projection parent", [][]string{
		{"e", root.ID, "", "root"},
	})
	selected := signedMutationEvent(t, nostrx.KindTextNote, "projection selected", [][]string{
		{"e", root.ID, "", "root"},
		{"e", parent.ID, "", "reply"},
	})
	for _, event := range []nostrx.Event{root, parent, selected} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.BuildThreadGraphCache(ctx, root.ID, 500); err != nil {
		t.Fatal(err)
	}
	srv.markThreadHydrateContextWarmed(ctx, root.ID)
	srv.markThreadHydrateRepliesReady(ctx, root.ID)
	if state, gotRoot := srv.threadProjectionStatus(ctx, selected.ID); state != threadProjectionReady || gotRoot != root.ID {
		t.Fatalf("complete projection = (%s, %s), want (ready, %s)", state, gotRoot, root.ID)
	}

	if err := st.SaveThreadGraphCache(ctx, store.ThreadGraphCache{
		RootID: root.ID, EventIDs: []string{selected.ID},
		ParentByID:       map[string]string{selected.ID: parent.ID},
		LastReplyEventAt: selected.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if state, _ := srv.threadProjectionStatus(ctx, selected.ID); state != threadProjectionMiss {
		t.Fatalf("projection with missing parent = %s, want miss", state)
	}
}

func TestThreadProjectionStatusRebuildsStaleGraphFromSQLite(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	root := signedMutationEvent(t, nostrx.KindTextNote, "rebuild root", nil)
	first := signedMutationEvent(t, nostrx.KindTextNote, "first reply", [][]string{{"e", root.ID, "", "root"}})
	for _, event := range []nostrx.Event{root, first} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.BuildThreadGraphCache(ctx, root.ID, 500); err != nil {
		t.Fatal(err)
	}
	srv.markThreadHydrateContextWarmed(ctx, root.ID)
	srv.markThreadHydrateRepliesReady(ctx, root.ID)

	newer := signedMutationEvent(t, nostrx.KindTextNote, "new reply invalidates graph", [][]string{{"e", root.ID, "", "root"}})
	newer.CreatedAt = first.CreatedAt + 1
	if err := st.SaveEvent(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if state, _ := srv.threadProjectionStatus(ctx, newer.ID); state != threadProjectionReady {
		t.Fatalf("rebuilt projection = %s, want ready", state)
	}
	cache, fresh, err := st.ThreadGraphCache(ctx, root.ID)
	if err != nil || cache == nil || !fresh {
		t.Fatalf("rebuilt graph = %#v fresh=%v err=%v", cache, fresh, err)
	}
	if !containsString(cache.EventIDs, newer.ID) {
		t.Fatalf("rebuilt graph omitted newly stored reply %s", newer.ID)
	}
}

func TestDesktopHydrateRepairsIncompleteGraphBeforePaintingReply(t *testing.T) {
	srv, st := testServer(t)
	srv.cfg.DesktopMode = true
	ctx := context.Background()
	root := signedMutationEvent(t, nostrx.KindTextNote, "hydrate repair root", nil)
	parent := signedMutationEvent(t, nostrx.KindTextNote, "hydrate repair parent", [][]string{
		{"e", root.ID, "", "root"},
	})
	selected := signedMutationEvent(t, nostrx.KindTextNote, "hydrate repair selected", [][]string{
		{"e", root.ID, "", "root"},
		{"e", parent.ID, "", "reply"},
	})
	for _, event := range []nostrx.Event{root, parent, selected} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveThreadGraphCache(ctx, store.ThreadGraphCache{
		RootID: root.ID, EventIDs: []string{selected.ID},
		ParentByID:       map[string]string{selected.ID: parent.ID},
		LastReplyEventAt: selected.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	srv.markThreadHydrateContextWarmed(ctx, root.ID)
	srv.markThreadHydrateRepliesReady(ctx, root.ID)

	req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID+"?fragment=hydrate&selected="+selected.ID, nil)
	req.Header.Set(headerViewerPubkey, strings.Repeat("f", 64))
	rec := httptest.NewRecorder()
	srv.handleThread(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Ptxt-Thread-Cache"); got == "stale" {
		t.Fatalf("incomplete selected path was mislabeled stale")
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="comment thread-focus-parent"`) || !strings.Contains(body, `id="note-`+parent.ID+`"`) {
		t.Fatalf("hydrate painted selected reply without its parent: %q", body)
	}
	if !strings.Contains(body, `id="note-`+selected.ID+`"`) {
		t.Fatalf("hydrate omitted selected reply: %q", body)
	}
}

func TestReadyAndStaleHydratesExposeStoreCacheState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		markReady bool
		want      string
	}{
		{name: "ready", markReady: true, want: "ready"},
		{name: "stale", markReady: false, want: "stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, st := testServer(t)
			srv.cfg.DesktopMode = true
			ctx := context.Background()
			root := signedMutationEvent(t, nostrx.KindTextNote, "stored hydrate "+tc.name, nil)
			reply := signedMutationEvent(t, nostrx.KindTextNote, "stored reply "+tc.name, [][]string{
				{"e", root.ID, "", "root"},
				{"p", root.PubKey},
			})
			for _, event := range []nostrx.Event{root, reply} {
				if err := st.SaveEvent(ctx, event); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := st.BuildThreadGraphCache(ctx, root.ID, 500); err != nil {
				t.Fatal(err)
			}
			if tc.markReady {
				srv.markThreadHydrateContextWarmed(ctx, root.ID)
				srv.markThreadHydrateRepliesReady(ctx, root.ID)
			}

			req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID+"?fragment=hydrate&selected="+reply.ID, nil)
			req.Header.Set(headerViewerPubkey, strings.Repeat("f", 64))
			rec := httptest.NewRecorder()
			srv.handleThread(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("X-Ptxt-Thread-Cache"); got != tc.want {
				t.Fatalf("cache header = %q, want %q", got, tc.want)
			}
			if timing := rec.Header().Get("Server-Timing"); !strings.Contains(timing, `thread-cache;desc="`+tc.want+`"`) {
				t.Fatalf("Server-Timing = %q, want cache state", timing)
			}
			if !strings.Contains(rec.Body.String(), reply.Content) {
				t.Fatalf("store hydrate omitted selected reply: %q", rec.Body.String())
			}
		})
	}
}
