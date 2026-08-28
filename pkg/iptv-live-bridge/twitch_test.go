package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchAndMakeAbsoluteM3U8(t *testing.T) {
	// Mock upstream CDN server returning relative HLS paths
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		body := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXTINF:2.000,
segment0.ts
#EXTINF:2.000,
sub/segment1.ts
#EXTINF:2.000,
https://other-cdn.example.com/segment2.ts
`
		w.Write([]byte(body))
	}))
	defer server.Close()

	ctx := context.Background()
	m3u8, err := FetchAndMakeAbsoluteM3U8(ctx, server.URL+"/live/playlist.m3u8")
	if err != nil {
		t.Fatalf("FetchAndMakeAbsoluteM3U8 failed: %v", err)
	}

	// Verify relative paths were rewritten to absolute URLs matching server.URL
	expectedSeg0 := server.URL + "/live/segment0.ts"
	if !strings.Contains(m3u8, expectedSeg0) {
		t.Errorf("expected relative segment rewritten to '%s', got:\n%s", expectedSeg0, m3u8)
	}

	expectedSeg1 := server.URL + "/live/sub/segment1.ts"
	if !strings.Contains(m3u8, expectedSeg1) {
		t.Errorf("expected relative segment rewritten to '%s', got:\n%s", expectedSeg1, m3u8)
	}

	// Verify already-absolute URL was preserved unchanged
	expectedSeg2 := "https://other-cdn.example.com/segment2.ts"
	if !strings.Contains(m3u8, expectedSeg2) {
		t.Errorf("expected absolute segment preserved '%s', got:\n%s", expectedSeg2, m3u8)
	}
}

func TestCreatorCirclesConfig(t *testing.T) {
	// Verify critical fallbacks exist
	if len(creatorCircles["tetris"]) == 0 {
		t.Error("expected tetris fallback circle")
	}
	if len(creatorCircles["smallant"]) == 0 {
		t.Error("expected smallant fallback circle")
	}
}
