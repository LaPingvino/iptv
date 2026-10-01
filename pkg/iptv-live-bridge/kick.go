package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Kick channels (/iptv/kick/<channel>): Kick runs on Amazon IVS. Its API gives
// a master playlist URL with a 10-minute token, so the bridge resolves it per
// request (cached for a minute) and serves the master playlist itself; the
// variant playlists inside are separately signed session URLs.

var kickCache = struct {
	sync.Mutex
	m map[string]kickEntry
}{m: map[string]kickEntry{}}

type kickEntry struct {
	master []byte
	at     time.Time
}

const kickUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36"

func kickMaster(ctx context.Context, channel string) ([]byte, error) {
	channel = strings.ToLower(channel)
	kickCache.Lock()
	if e, ok := kickCache.m[channel]; ok && time.Since(e.at) < time.Minute {
		kickCache.Unlock()
		return e.master, nil
	}
	kickCache.Unlock()

	get := func(u, accept string) ([]byte, error) {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
		req.Header.Set("User-Agent", kickUA)
		req.Header.Set("Accept", accept)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, u)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	}

	b, err := get("https://kick.com/api/v2/channels/"+channel, "application/json")
	if err != nil {
		return nil, err
	}
	var ch struct {
		PlaybackURL string          `json:"playback_url"`
		Livestream  json.RawMessage `json:"livestream"`
	}
	if err := json.Unmarshal(b, &ch); err != nil {
		return nil, err
	}
	if ch.PlaybackURL == "" || len(ch.Livestream) == 0 || string(ch.Livestream) == "null" {
		return nil, fmt.Errorf("kick channel %s is offline", channel)
	}
	master, err := get(ch.PlaybackURL, "*/*")
	if err != nil {
		return nil, err
	}
	kickCache.Lock()
	kickCache.m[channel] = kickEntry{master: master, at: time.Now()}
	kickCache.Unlock()
	return master, nil
}

func serveKick(w http.ResponseWriter, r *http.Request, channel string) {
	master, err := kickMaster(r.Context(), channel)
	if err != nil {
		serveOfflineSlate(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(master)
}
