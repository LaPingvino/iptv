package main

import (
	"bytes"
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
	if len(creatorCircles["tasvideos"]) == 0 {
		t.Error("expected tasvideos fallback circle")
	}
}

func TestSniffMpegTsPTS(t *testing.T) {
	packet := make([]byte, 188)
	packet[0] = 0x47 // Sync byte
	packet[1] = 0x40 // PUSI = 1, PID = 0
	packet[2] = 0x20
	packet[3] = 0x10 // AFC = 1 (payload only)

	// PES header starting at packet[4]
	packet[4] = 0x00
	packet[5] = 0x00
	packet[6] = 0x01
	packet[7] = 0xE0 // Video stream ID
	packet[8] = 0x00 // Length
	packet[9] = 0x00
	packet[10] = 0x80 // Flags 1
	packet[11] = 0x80 // Flags 2 (PTS present, bits 7-6 = 10)
	packet[12] = 0x05 // Header data length = 5 bytes

	// Encode known PTS: 3523142880
	var expectedPTS uint64 = 3523142880
	packet[13] = 0x20 | byte(((expectedPTS>>30)&0x07)<<1) | 0x01
	packet[14] = byte((expectedPTS >> 22) & 0xFF)
	packet[15] = byte(((expectedPTS>>15)&0x7F)<<1) | 0x01
	packet[16] = byte((expectedPTS >> 7) & 0xFF)
	packet[17] = byte((expectedPTS&0x7F)<<1) | 0x01

	pts, err := SniffMpegTsPTS(bytes.NewReader(packet))
	if err != nil {
		t.Fatalf("SniffMpegTsPTS failed: %v", err)
	}
	if pts != expectedPTS {
		t.Fatalf("expected PTS %d, got %d", expectedPTS, pts)
	}
}

func TestTwitchSubtitleStateAndSegGeneration(t *testing.T) {
	channel := "teststreamer"
	m3u8 := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:1000
#EXT-X-TWITCH-ELAPSED-SECS:2000.000
#EXTINF:2.000,live
https://example.com/seg1000.ts
#EXTINF:2.000,live
https://example.com/seg1001.ts
#EXTINF:2.000,live
https://example.com/seg1002.ts
`
	UpdateTwitchSubState(channel, m3u8, "")
	st := GetTwitchSubState(channel)
	if st == nil {
		t.Fatalf("expected TwitchSubState for %s, got nil", channel)
	}
	if st.MediaSequence != 1000 {
		t.Errorf("expected media sequence 1000, got %d", st.MediaSequence)
	}
	if len(st.Segments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(st.Segments))
	}
	if st.Segments[0].Seq != 1000 || st.Segments[1].Seq != 1001 || st.Segments[2].Seq != 1002 {
		t.Errorf("unexpected segment sequences: %+v", st.Segments)
	}

	// Verify formatVTTTime
	if res := formatVTTTime(2.0); res != "00:00:02.000" {
		t.Errorf("expected 00:00:02.000, got %s", res)
	}
	if res := formatNumber(1420); res != "1.4k" {
		t.Errorf("expected 1.4k, got %s", res)
	}
}

func TestTwitchMasterAndSubPlaylistEndpoints(t *testing.T) {
	// 1. Test Master Playlist generation
	req := httptest.NewRequest(http.MethodGet, "/iptv/twitch/speedrun?bias=romhack", nil)
	rr := httptest.NewRecorder()
	serveTwitchMasterM3U8(rr, req, "speedrun")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	masterContent := rr.Body.String()
	if !strings.Contains(masterContent, `TYPE=SUBTITLES`) {
		t.Errorf("expected TYPE=SUBTITLES in master playlist, got:\n%s", masterContent)
	}
	if !strings.Contains(masterContent, `/iptv/twitch/sub/speedrun.m3u8?bias=romhack`) {
		t.Errorf("expected sub URI in master playlist, got:\n%s", masterContent)
	}
	if !strings.Contains(masterContent, `/iptv/twitch/video/speedrun.m3u8?bias=romhack`) {
		t.Errorf("expected video URI in master playlist, got:\n%s", masterContent)
	}

	// 2. Test Subtitle Media Playlist generation
	reqSub := httptest.NewRequest(http.MethodGet, "/iptv/twitch/sub/speedrun.m3u8", nil)
	rrSub := httptest.NewRecorder()
	serveTwitchSubM3U8(rrSub, reqSub, "speedrun", "")
	if rrSub.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrSub.Code)
	}
	if !strings.Contains(rrSub.Body.String(), "#EXTM3U") {
		t.Errorf("expected #EXTM3U in subtitle playlist, got:\n%s", rrSub.Body.String())
	}

	// 3. Test Subtitle Segment generation
	reqSeg := httptest.NewRequest(http.MethodGet, "/iptv/twitch/subseg/speedrun/1000.vtt", nil)
	rrSeg := httptest.NewRecorder()
	serveTwitchSubSeg(rrSeg, reqSeg, "speedrun", 1000)
	if rrSeg.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrSeg.Code)
	}
	segContent := rrSeg.Body.String()
	if !strings.Contains(segContent, "WEBVTT") || !strings.Contains(segContent, "X-TIMESTAMP-MAP=") {
		t.Errorf("expected WEBVTT with X-TIMESTAMP-MAP, got:\n%s", segContent)
	}
}
