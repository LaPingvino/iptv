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

type RaidMemory struct {
	target    string
	expiresAt time.Time
}

type TwitchManager struct {
	mu           sync.RWMutex
	cache        map[string]CachedStream
	cacheTTL     time.Duration
	session      *streamglink.Streamlink
	raidMemories map[string]RaidMemory
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

var twitchMgr = &TwitchManager{
	cache:        make(map[string]CachedStream),
	cacheTTL:     300 * time.Second,
	session:      streamglink.New(),
	raidMemories: make(map[string]RaidMemory),
}

// FallbackInfo holds discovery signals from Twitch GQL
type FallbackInfo struct {
	IsLive       bool
	RaidTarget   string
	HostTarget   string
	TeamName     string
	Teammates    []string
	LastGameName string
}

func (tm *TwitchManager) fetchChannelFallbackInfo(ctx context.Context, channel string) (*FallbackInfo, error) {
	rawQuery := `
	query AutonomousFallbackProbe($login: String!) {
	  user(login: $login) {
	    stream {
	      id
	    }
	    raid {
	      targetChannel {
	        login
	      }
	    }
	    hosting {
	      login
	      stream { id }
	    }
	    primaryTeam {
	      displayName
	      members {
	        edges {
	          node {
	            login
	            stream {
	              viewersCount
	            }
	          }
	        }
	      }
	    }
	    lastBroadcast {
	      game {
	        name
	      }
	    }
	  }
	}`

	payload := map[string]any{
		"query": rawQuery,
		"variables": map[string]string{
			"login": channel,
		},
	}
	pBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://gql.twitch.tv/gql", strings.NewReader(string(pBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Client-Id", "kimne78kx3ncx6brgo4mv6wki5h1ko")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res struct {
		Data struct {
			User *struct {
				Stream *struct {
					ID string `json:"id"`
				} `json:"stream"`
				Raid *struct {
					TargetChannel *struct {
						Login string `json:"login"`
					} `json:"targetChannel"`
				} `json:"raid"`
				Hosting *struct {
					Login  string `json:"login"`
					Stream *struct {
						ID string `json:"id"`
					} `json:"stream"`
				} `json:"hosting"`
				PrimaryTeam *struct {
					DisplayName string `json:"displayName"`
					Members     struct {
						Edges []struct {
							Node struct {
								Login  string `json:"login"`
								Stream *struct {
									ViewersCount int `json:"viewersCount"`
								} `json:"stream"`
							} `json:"node"`
						} `json:"edges"`
					} `json:"members"`
				} `json:"primaryTeam"`
				LastBroadcast *struct {
					Game *struct {
						Name string `json:"name"`
					} `json:"game"`
				} `json:"lastBroadcast"`
			} `json:"user"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	if res.Data.User == nil {
		return nil, fmt.Errorf("user %s not found on Twitch", channel)
	}

	u := res.Data.User
	info := &FallbackInfo{
		IsLive: u.Stream != nil,
	}

	if u.Raid != nil && u.Raid.TargetChannel != nil {
		info.RaidTarget = strings.ToLower(u.Raid.TargetChannel.Login)
	}

	if u.Hosting != nil && u.Hosting.Stream != nil && u.Hosting.Login != "" {
		info.HostTarget = strings.ToLower(u.Hosting.Login)
	}

	if u.PrimaryTeam != nil {
		info.TeamName = u.PrimaryTeam.DisplayName
		// Sort teammates by viewer count descending
		type liveTeammate struct {
			login   string
			viewers int
		}
		var list []liveTeammate
		for _, e := range u.PrimaryTeam.Members.Edges {
			if e.Node.Stream != nil && strings.ToLower(e.Node.Login) != channel {
				list = append(list, liveTeammate{
					login:   strings.ToLower(e.Node.Login),
					viewers: e.Node.Stream.ViewersCount,
				})
			}
		}
		// Sort descending
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				if list[j].viewers > list[i].viewers {
					list[i], list[j] = list[j], list[i]
				}
			}
		}
		for _, lt := range list {
			info.Teammates = append(info.Teammates, lt.login)
		}
	}

	if u.LastBroadcast != nil && u.LastBroadcast.Game != nil {
		info.LastGameName = u.LastBroadcast.Game.Name
	}

	return info, nil
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

	// Query Twitch GQL fallback & live signals in one fast call
	info, _ := tm.fetchChannelFallbackInfo(ctx, channel)

	if info != nil && info.RaidTarget != "" {
		tm.mu.Lock()
		tm.raidMemories[channel] = RaidMemory{
			target:    info.RaidTarget,
			expiresAt: time.Now().Add(2 * time.Hour),
		}
		tm.mu.Unlock()
	}

	// 1. Try primary channel if live or if GQL check was inconclusive
	if info == nil || info.IsLive {
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
	}

	// 2. Channel is offline: Check Recent Raid Target (active or cached within 2 hours)
	var raidTarget string
	if info != nil && info.RaidTarget != "" {
		raidTarget = info.RaidTarget
	} else {
		tm.mu.RLock()
		rm, exists := tm.raidMemories[channel]
		if exists && time.Now().Before(rm.expiresAt) {
			raidTarget = rm.target
		}
		tm.mu.RUnlock()
	}

	if raidTarget != "" {
		log.Printf("[Twitch] %s raided %s -> failing over to raid target", channel, raidTarget)
		raidURL, err := tm.resolveSingle(ctx, raidTarget)
		if err == nil {
			return raidURL, nil
		}
	}

	// 3. Check Channel Host Target
	if info != nil && info.HostTarget != "" {
		log.Printf("[Twitch] %s is hosting %s -> failing over to host", channel, info.HostTarget)
		hostURL, err := tm.resolveSingle(ctx, info.HostTarget)
		if err == nil {
			return hostURL, nil
		}
	}

	// 4. Check Live Teammates (Primary Team)
	if info != nil && len(info.Teammates) > 0 {
		for _, teammate := range info.Teammates {
			teammateURL, err := tm.resolveSingle(ctx, teammate)
			if err == nil {
				log.Printf("[Twitch] %s offline -> routed to live teammate %s (%s)", channel, teammate, info.TeamName)
				return teammateURL, nil
			}
		}
	}

	// 5. Check Contextual Last Broadcast Game Category
	if info != nil && info.LastGameName != "" {
		gameURL, err := tm.ResolveGame(ctx, info.LastGameName, "")
		if err == nil && gameURL != "" {
			log.Printf("[Twitch] %s offline -> routed to top streamer in last played game '%s'", channel, info.LastGameName)
			return gameURL, nil
		}
	}

	// 6. Check Curated Creator Circles Safety Net
	if circle, exists := creatorCircles[channel]; exists {
		for _, fb := range circle {
			fbURL, fbErr := tm.resolveSingle(ctx, fb)
			if fbErr == nil {
				log.Printf("[Twitch] %s offline -> routed to creator circle fallback %s", channel, fb)
				return fbURL, nil
			}
		}
	}

	return "", fmt.Errorf("channel %s and all fallbacks offline", channel)
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
