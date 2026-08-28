package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/LaPingvino/streamglink"
	_ "github.com/LaPingvino/streamglink/plugins/generic"
	_ "github.com/LaPingvino/streamglink/plugins/twitch"
)

type CachedStream struct {
	URL       string
	ExpiresAt time.Time
}

type TwitchManager struct {
	mu       sync.RWMutex
	cache    map[string]CachedStream
	cacheTTL time.Duration
	session  *streamglink.Streamlink
}

var twitchMgr = &TwitchManager{
	cache:    make(map[string]CachedStream),
	cacheTTL: 300 * time.Second,
	session:  streamglink.New(),
}

// Creator Circles for fallback when primary stream is offline
var creatorCircles = map[string][]string{
	"tetris":           {"harddrop", "classictetris", "dogplayingtetris", "fractal", "ericicx", "alex_t", "bluescuti", "wumbotize", "doremy", "speedrun"},
	"nes-tetris":       {"classictetris", "dogplayingtetris", "fractal", "ericicx", "alex_t", "bluescuti", "harddrop", "speedrun"},
	"classictetris":    {"dogplayingtetris", "fractal", "ericicx", "alex_t", "bluescuti", "classictetris2", "harddrop", "speedrun"},
	"smallant":         {"dgr_dave", "ryukahr", "speedrun", "thabeast721", "grandpoobear"},
	"ryukahr":          {"tamthegamer", "dgr_dave", "smallant", "thabeast721", "aurateur"},
	"carlsagan42":      {"juzcook", "dgr_dave", "grandpoobear", "thabeast721", "aurateur"},
	"thabeast721":      {"grandpoobear", "aurateur", "pangaeapanga", "simpleflips", "carlsagan42"},
	"grandpoobear":     {"thabeast721", "aurateur", "carlsagan42", "juzcook", "pangaeapanga"},
	"gamesdonequick":   {"esamarathon", "speedrun", "tasvideos"},
	"speedrun":         {"gamesdonequick", "esamarathon", "tasvideos"},
}

func (tm *TwitchManager) Resolve(ctx context.Context, channel string) (string, error) {
	channel = strings.ToLower(strings.TrimSpace(channel))
	tm.mu.RLock()
	cached, ok := tm.cache[channel]
	if ok && time.Now().Before(cached.ExpiresAt) {
		tm.mu.RUnlock()
		return cached.URL, nil
	}
	tm.mu.RUnlock()

	// Try primary channel
	streamURL, err := tm.resolveSingle(ctx, channel)
	if err == nil {
		tm.mu.Lock()
		tm.cache[channel] = CachedStream{
			URL:       streamURL,
			ExpiresAt: time.Now().Add(tm.cacheTTL),
		}
		tm.mu.Unlock()
		return streamURL, nil
	}

	// Try creator circle fallback
	if circle, exists := creatorCircles[channel]; exists {
		for _, fb := range circle {
			fbURL, fbErr := tm.resolveSingle(ctx, fb)
			if fbErr == nil {
				log.Printf("[Twitch] Fallback: %s is offline, routed to %s", channel, fb)
				return fbURL, nil
			}
		}
	}

	return "", err
}

func (tm *TwitchManager) resolveSingle(ctx context.Context, channel string) (string, error) {
	targetURL := fmt.Sprintf("https://www.twitch.tv/%s", channel)
	st, err := tm.session.Best(ctx, targetURL)
	if err != nil {
		return "", err
	}
	return st.URL(), nil
}

func (tm *TwitchManager) Invalidate(channel string) {
	tm.mu.Lock()
	delete(tm.cache, strings.ToLower(channel))
	tm.mu.Unlock()
}

// FetchAndMakeAbsoluteM3U8 fetches the HLS playlist and rewrites relative segment paths to absolute URLs.
func FetchAndMakeAbsoluteM3U8(ctx context.Context, targetURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("CDN error HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	baseURL, err := url.Parse(targetURL)
	if err != nil {
		return string(body), nil
	}

	lines := strings.Split(string(body), "\n")
	var out []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			segURL, err := baseURL.Parse(trimmed)
			if err == nil {
				out = append(out, segURL.String())
				continue
			}
		}
		out = append(out, line)
	}

	return strings.Join(out, "\n"), nil
}
