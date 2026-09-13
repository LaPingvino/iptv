package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRewriteM3U8MasterPlaylist(t *testing.T) {
	masterM3U8 := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio1",NAME="Français",DEFAULT=YES,LANGUAGE="fr",URI="audio.m3u8"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="Nederlands",LANGUAGE="nl",URI="subs_nl.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=3150440,RESOLUTION=1280x720
master_v720.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1789512,RESOLUTION=960x540
https://example.com/live/master_v540.m3u8
`
	upstreamURL := "https://example.com/live/index.m3u8"
	rewritten, err := RewriteM3U8(masterM3U8, upstreamURL)
	if err != nil {
		t.Fatalf("RewriteM3U8 failed: %v", err)
	}

	// Verify audio track rewriting
	expectedAudioTarget := "https://example.com/live/audio.m3u8"
	expectedAudioToken := base64.RawURLEncoding.EncodeToString([]byte(expectedAudioTarget))
	expectedAudioURI := fmt.Sprintf(`URI="/iptv/hls/m/%s/playlist.m3u8"`, expectedAudioToken)
	if !strings.Contains(rewritten, expectedAudioURI) {
		t.Errorf("rewritten master missing expected audio URI: %s\nGot:\n%s", expectedAudioURI, rewritten)
	}

	// Verify subtitle track rewriting
	expectedSubsTarget := "https://example.com/live/subs_nl.m3u8"
	expectedSubsToken := base64.RawURLEncoding.EncodeToString([]byte(expectedSubsTarget))
	expectedSubsURI := fmt.Sprintf(`URI="/iptv/hls/m/%s/playlist.m3u8"`, expectedSubsToken)
	if !strings.Contains(rewritten, expectedSubsURI) {
		t.Errorf("rewritten master missing expected subs URI: %s\nGot:\n%s", expectedSubsURI, rewritten)
	}

	// Verify relative variant rewriting
	expectedVar1Target := "https://example.com/live/master_v720.m3u8"
	expectedVar1Token := base64.RawURLEncoding.EncodeToString([]byte(expectedVar1Target))
	expectedVar1URI := fmt.Sprintf("/iptv/hls/m/%s/playlist.m3u8", expectedVar1Token)
	if !strings.Contains(rewritten, expectedVar1URI) {
		t.Errorf("rewritten master missing expected variant 1 URI: %s\nGot:\n%s", expectedVar1URI, rewritten)
	}

	// Verify absolute variant rewriting
	expectedVar2Target := "https://example.com/live/master_v540.m3u8"
	expectedVar2Token := base64.RawURLEncoding.EncodeToString([]byte(expectedVar2Target))
	expectedVar2URI := fmt.Sprintf("/iptv/hls/m/%s/playlist.m3u8", expectedVar2Token)
	if !strings.Contains(rewritten, expectedVar2URI) {
		t.Errorf("rewritten master missing expected variant 2 URI: %s\nGot:\n%s", expectedVar2URI, rewritten)
	}
}

func TestRewriteM3U8MediaPlaylist(t *testing.T) {
	mediaM3U8 := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:6
#EXT-X-MEDIA-SEQUENCE:100
#EXTINF:6.0,
segment100.ts
#EXTINF:6.0,
sub/segment101.ts
#EXTINF:6.0,
https://cdn.example.com/live/segment102.ts
`
	upstreamURL := "https://example.com/live/master_v720.m3u8"
	rewritten, err := RewriteM3U8(mediaM3U8, upstreamURL)
	if err != nil {
		t.Fatalf("RewriteM3U8 failed: %v", err)
	}

	// Verify segment 100
	seg1Target := "https://example.com/live/segment100.ts"
	seg1Token := base64.RawURLEncoding.EncodeToString([]byte(seg1Target))
	expectedSeg1 := fmt.Sprintf("/iptv/hls/s/%s/segment.ts", seg1Token)
	if !strings.Contains(rewritten, expectedSeg1) {
		t.Errorf("rewritten media missing expected seg 1: %s\nGot:\n%s", expectedSeg1, rewritten)
	}

	// Verify segment 101 (relative subdirectory)
	seg2Target := "https://example.com/live/sub/segment101.ts"
	seg2Token := base64.RawURLEncoding.EncodeToString([]byte(seg2Target))
	expectedSeg2 := fmt.Sprintf("/iptv/hls/s/%s/segment.ts", seg2Token)
	if !strings.Contains(rewritten, expectedSeg2) {
		t.Errorf("rewritten media missing expected seg 2: %s\nGot:\n%s", expectedSeg2, rewritten)
	}

	// Verify segment 102 (absolute URL)
	seg3Target := "https://cdn.example.com/live/segment102.ts"
	seg3Token := base64.RawURLEncoding.EncodeToString([]byte(seg3Target))
	expectedSeg3 := fmt.Sprintf("/iptv/hls/s/%s/segment.ts", seg3Token)
	if !strings.Contains(rewritten, expectedSeg3) {
		t.Errorf("rewritten media missing expected seg 3: %s\nGot:\n%s", expectedSeg3, rewritten)
	}
}

func TestResolveProxiedChannel(t *testing.T) {
	testCases := []struct {
		Path       string
		ExpectedID string
	}{
		{"arte.m3u8", "arte_fr"},
		{"/iptv/arte_fr.m3u8", "arte_fr"},
		{"arte_de", "arte_de"},
		{"tv5monde.m3u8", "tv5monde_europe"},
		{"tv5monde_info.m3u8", "tv5monde_info"},
		{"zdf.m3u8", "zdf"},
		{"zdfneo.m3u8", "zdfneo"},
		{"3sat.m3u8", "3sat"},
		{"phoenix.m3u8", "phoenix"},
		{"kika.m3u8", "kika"},
		{"tagesschau24.m3u8", "tagesschau24"},
		{"wdr.m3u8", "wdr"},
	}

	for _, tc := range testCases {
		ch, ok := ResolveProxiedChannel(tc.Path)
		if !ok {
			t.Errorf("ResolveProxiedChannel(%q) returned false", tc.Path)
			continue
		}
		if ch.ID != tc.ExpectedID {
			t.Errorf("ResolveProxiedChannel(%q) = %q, expected %q", tc.Path, ch.ID, tc.ExpectedID)
		}
	}
}

func TestProxyHTTPFlow(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nvariant.m3u8\n"))
		case "/live/variant.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write([]byte("#EXTM3U\n#EXTINF:6.0,\nseg1.ts\n"))
		case "/live/seg1.ts":
			w.Header().Set("Content-Type", "video/MP2T")
			w.Header().Set("Content-Length", "7")
			w.Write([]byte("TSCHUNK"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	// 1. Fetch master playlist
	ch := ProxiedChannel{
		ID:          "mock_ch",
		UpstreamURL: mockServer.URL + "/live/index.m3u8",
	}

	recMaster := httptest.NewRecorder()
	reqMaster := httptest.NewRequest(http.MethodGet, "/iptv/mock_ch.m3u8", nil)
	HandleProxiedChannel(recMaster, reqMaster, ch)

	if recMaster.Code != http.StatusOK {
		t.Fatalf("HandleProxiedChannel returned %d", recMaster.Code)
	}

	masterBody := recMaster.Body.String()
	if !strings.Contains(masterBody, "/iptv/hls/m/") {
		t.Fatalf("Master playlist did not contain proxied variant link: %s", masterBody)
	}

	// 2. Extract token for variant
	lines := strings.Split(masterBody, "\n")
	var variantURI string
	for _, l := range lines {
		if strings.HasPrefix(l, "/iptv/hls/m/") {
			variantURI = l
			break
		}
	}
	if variantURI == "" {
		t.Fatalf("Could not find variant URI in: %s", masterBody)
	}

	token := strings.TrimPrefix(variantURI, "/iptv/hls/m/")
	token = strings.TrimSuffix(token, "/playlist.m3u8")

	recVariant := httptest.NewRecorder()
	reqVariant := httptest.NewRequest(http.MethodGet, variantURI, nil)
	HandleHLSManifest(recVariant, reqVariant, token)

	if recVariant.Code != http.StatusOK {
		t.Fatalf("HandleHLSManifest returned %d", recVariant.Code)
	}

	variantBody := recVariant.Body.String()
	if !strings.Contains(variantBody, "/iptv/hls/s/") {
		t.Fatalf("Variant playlist did not contain proxied segment link: %s", variantBody)
	}

	// 3. Extract token for segment and fetch
	var segURI string
	for _, l := range strings.Split(variantBody, "\n") {
		if strings.HasPrefix(l, "/iptv/hls/s/") {
			segURI = l
			break
		}
	}
	if segURI == "" {
		t.Fatalf("Could not find segment URI in: %s", variantBody)
	}

	segToken := strings.TrimPrefix(segURI, "/iptv/hls/s/")
	segToken = strings.TrimSuffix(segToken, "/segment.ts")

	recSeg := httptest.NewRecorder()
	reqSeg := httptest.NewRequest(http.MethodGet, segURI, nil)
	HandleHLSSegment(recSeg, reqSeg, segToken)

	if recSeg.Code != http.StatusOK {
		t.Fatalf("HandleHLSSegment returned %d", recSeg.Code)
	}

	if recSeg.Body.String() != "TSCHUNK" {
		t.Fatalf("HandleHLSSegment body mismatch: expected TSCHUNK, got %q", recSeg.Body.String())
	}
}

func TestLiveRealUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real network test in short mode")
	}

	channelsToTest := []string{"arte_fr", "arte_de", "tv5monde_europe", "zdf", "3sat", "wdr"}
	for _, id := range channelsToTest {
		ch, ok := ProxiedChannels[id]
		if !ok {
			t.Fatalf("channel %s not found in ProxiedChannels", id)
		}

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/iptv/"+id+".m3u8", nil)
		HandleProxiedChannel(rec, req, ch)

		if rec.Code != http.StatusOK {
			t.Errorf("HandleProxiedChannel for %s returned HTTP %d", id, rec.Code)
			continue
		}

		body := rec.Body.String()
		if !strings.Contains(body, "#EXTM3U") {
			t.Errorf("channel %s response does not start with #EXTM3U:\n%s", id, body[:min(200, len(body))])
		}
		if !strings.Contains(body, "/iptv/hls/m/") && !strings.Contains(body, "/iptv/hls/s/") {
			t.Errorf("channel %s response does not contain proxied links:\n%s", id, body[:min(300, len(body))])
		}

		// For arte_fr, test full chain down to an actual .ts segment
		if id == "arte_fr" {
			var variantURI string
			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(line, "/iptv/hls/m/") {
					variantURI = line
					break
				}
			}
			if variantURI == "" {
				t.Fatalf("no variant URI found in arte_fr master playlist")
			}
			varToken := strings.TrimSuffix(strings.TrimPrefix(variantURI, "/iptv/hls/m/"), "/playlist.m3u8")

			recVar := httptest.NewRecorder()
			reqVar := httptest.NewRequest(http.MethodGet, variantURI, nil)
			HandleHLSManifest(recVar, reqVar, varToken)
			if recVar.Code != http.StatusOK {
				t.Fatalf("HandleHLSManifest for variant failed: HTTP %d", recVar.Code)
			}

			varSegBody := recVar.Body.String()
			var segURI string
			for _, line := range strings.Split(varSegBody, "\n") {
				if strings.HasPrefix(line, "/iptv/hls/s/") {
					segURI = line
					break
				}
			}
			if segURI == "" {
				t.Fatalf("no segment URI found in arte_fr variant playlist")
			}
			segToken := strings.TrimSuffix(strings.TrimPrefix(segURI, "/iptv/hls/s/"), "/segment.ts")

			recSeg := httptest.NewRecorder()
			reqSeg := httptest.NewRequest(http.MethodGet, segURI, nil)
			HandleHLSSegment(recSeg, reqSeg, segToken)
			if recSeg.Code != http.StatusOK {
				t.Fatalf("HandleHLSSegment for real arte_fr segment failed: HTTP %d", recSeg.Code)
			}
			segBytes := recSeg.Body.Bytes()
			if len(segBytes) < 188 || segBytes[0] != 0x47 {
				t.Fatalf("real segment did not begin with MPEG-TS sync byte 0x47, len=%d", len(segBytes))
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestSOReusePortListener(t *testing.T) {
	// Test on ephemeral port 17555
	testPort := 17555
	l1, err := createListener(testPort)
	if err != nil {
		t.Fatalf("first createListener failed: %v", err)
	}
	defer l1.Close()

	// Second listener on same port must succeed due to SO_REUSEPORT
	l2, err := createListener(testPort)
	if err != nil {
		t.Fatalf("second createListener on same port failed (SO_REUSEPORT expected): %v", err)
	}
	defer l2.Close()
}

func TestReloadState(t *testing.T) {
	// Ensure reloadState executes cleanly without panics
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("reloadState panicked: %v", r)
		}
	}()
	reloadState(nil, nil)
}


