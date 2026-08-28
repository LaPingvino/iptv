package main

import (
	"context"
	"encoding/json"
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

var GameGroups = map[string][]string{
	"modern-tetris":   {"TETR.IO", "Tetris Effect: Connected", "Tetris Effect", "TETRIS 99", "Puyo Puyo Tetris 2", "Puyo Puyo Tetris"},
	"nes-tetris":      {"Tetris"},
	"mario-speedruns": {"Super Mario 64", "Super Mario World", "Super Mario Sunshine", "Super Mario Bros. 3", "Super Mario Odyssey"},
	"retro-rpg":       {"Chrono Trigger", "Final Fantasy VI", "EarthBound", "Secret of Mana"},
}

var romhackKeywords = []string{
	"kaizo", "romhack", "rom hack", "smw hack", "grand poo world", "quick boom box",
	"invictus", "learn 2 kaizo", "super dram world", "item abuse", "troll", "mario maker",
}

type gameStreamNode struct {
	ViewersCount int    `json:"viewersCount"`
	Title        string `json:"title"`
	Broadcaster  struct {
		Login string `json:"login"`
	} `json:"broadcaster"`
}

type gameGQLResponse struct {
	Data struct {
		Game struct {
			Streams struct {
				Edges []struct {
					Node gameStreamNode `json:"node"`
				} `json:"edges"`
			} `json:"streams"`
		} `json:"game"`
	} `json:"data"`
}

func (tm *TwitchManager) ResolveGame(ctx context.Context, gameName, bias string) (string, error) {
	cleanName := strings.ReplaceAll(gameName, "-", " ")
	cleanName, _ = url.QueryUnescape(cleanName)

	rawQuery := `
	query GetGameStreams($name: String!) {
	  game(name: $name) {
	    streams(first: 20) {
	      edges {
	        node {
	          viewersCount
	          title
	          broadcaster {
	            login
	          }
	        }
	      }
	    }
	  }
	}`

	payload := map[string]any{
		"query": rawQuery,
		"variables": map[string]string{
			"name": cleanName,
		},
	}
	pBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://gql.twitch.tv/gql", strings.NewReader(string(pBytes)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Client-Id", "kimne78kx3ncx6brgo4mv6wki5h1ko")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var res gameGQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	edges := res.Data.Game.Streams.Edges
	if len(edges) == 0 {
		return "", fmt.Errorf("no live streams found for game %s", cleanName)
	}

	var topLogin string
	maxViewers := 0

	// Check bias
	if bias == "romhack" || bias == "nes" {
		for _, e := range edges {
			n := e.Node
			lowerTitle := strings.ToLower(n.Title)
			matches := false
			for _, kw := range romhackKeywords {
				if strings.Contains(lowerTitle, kw) {
					matches = true
					break
				}
			}
			if matches && n.ViewersCount > maxViewers {
				maxViewers = n.ViewersCount
				topLogin = n.Broadcaster.Login
			}
		}
	}

	if topLogin == "" {
		for _, e := range edges {
			if e.Node.ViewersCount >= 2 {
				topLogin = e.Node.Broadcaster.Login
				break
			}
		}
	}

	if topLogin == "" && len(edges) > 0 {
		topLogin = edges[0].Node.Broadcaster.Login
	}

	if topLogin == "" {
		return "", fmt.Errorf("no suitable streamer for game %s", cleanName)
	}

	log.Printf("[Twitch] Resolved game '%s' -> streamer '%s'", cleanName, topLogin)
	return tm.Resolve(ctx, topLogin)
}

func (tm *TwitchManager) ResolveGroup(ctx context.Context, groupName, bias string) (string, error) {
	groupName = strings.ToLower(strings.TrimSpace(groupName))
	games, ok := GameGroups[groupName]
	if !ok {
		return tm.ResolveGame(ctx, groupName, bias)
	}

	for _, g := range games {
		u, err := tm.ResolveGame(ctx, g, bias)
		if err == nil && u != "" {
			return u, nil
		}
	}

	return "", fmt.Errorf("no active streams for group %s", groupName)
}
