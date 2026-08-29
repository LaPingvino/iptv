package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/LaPingvino/streamglink"
	_ "github.com/LaPingvino/streamglink/plugins/generic"
	_ "github.com/LaPingvino/streamglink/plugins/twitch"
)

type FollowedStreamer struct {
	BroadcasterID    string `json:"broadcaster_id"`
	BroadcasterLogin string `json:"broadcaster_login"`
	BroadcasterName  string `json:"broadcaster_name"`
}

var (
	lapingvinoFollowsMu   sync.RWMutex
	lapingvinoFollowsList []string
	lapingvinoFollowsSet  = make(map[string]bool)
	liveFollowsCache      []string
	liveFollowsCacheTime  time.Time

	dedicatedStreamersMu sync.RWMutex
	dedicatedStreamers   = map[string]bool{
		"alex_t":           true,
		"ambercyprian":     true,
		"aurateur":         true,
		"barbarousking":    true,
		"bluescuti":        true,
		"bobross":          true,
		"carlsagan42":      true,
		"carrarium":        true,
		"classictetris":    true,
		"classictetris2":   true,
		"classictetris3":   true,
		"classictetris4":   true,
		"dgr_dave":         true,
		"dogplayingtetris": true,
		"doremy":           true,
		"elanaorama":       true,
		"ericicx":          true,
		"esamarathon":      true,
		"failstream":       true,
		"fractal":          true,
		"gamesdonequick":   true,
		"glitchcat7":       true,
		"grandpoobear":     true,
		"harddrop":         true,
		"insomniac":        true,
		"juzcook":          true,
		"leekbeats":        true,
		"lofigirl":         true,
		"mitchflowerpower": true,
		"monstercat":       true,
		"mst3k":            true,
		"msushi":           true,
		"nasa":             true,
		"pangaeapanga":     true,
		"rbpimlico":        true,
		"relaxbeats":       true,
		"ryukahr":          true,
		"shoutfactorytv":   true,
		"shoujo":           true,
		"simpleflips":      true,
		"smallant":         true,
		"speedrun":         true,
		"tammy_blackmedia": true,
		"tasvideos":        true,
		"tgh_sr":           true,
		"thabeast721":      true,
		"vinesandwillows":  true,
		"worldoflongplays": true,
		"wumbotize":        true,
	}
)

func init() {
	loadLapingvinoFollows()
	loadDedicatedStreamers()
}

func isDedicatedStreamer(login string) bool {
	dedicatedStreamersMu.RLock()
	defer dedicatedStreamersMu.RUnlock()
	return dedicatedStreamers[strings.ToLower(login)]
}

func loadDedicatedStreamers() {
	paths := []string{
		filepath.Join(ProjectDir, "data"),
		filepath.Join(MediaDir, "data"),
		"/var/lib/iptv-live-bridge/data",
		"/usr/share/iptv-live-bridge/data",
		"/home/joop/iptv/data",
	}
	for _, dir := range paths {
		files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
		if err != nil || len(files) == 0 {
			continue
		}
		for _, f := range files {
			if strings.Contains(f, "24_followed_streamers.yaml") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var list []ChannelDef
			if err := yaml.Unmarshal(b, &list); err != nil {
				continue
			}
			for _, ch := range list {
				if strings.Contains(ch.URL, "twitch") {
					cleanURL := strings.Split(ch.URL, "?")[0]
					cleanURL = strings.TrimRight(cleanURL, "/")
					if strings.Contains(cleanURL, "/game/") || strings.Contains(cleanURL, "/group/") ||
						strings.Contains(cleanURL, "/followed/") || strings.Contains(cleanURL, "/auto-live") ||
						strings.Contains(cleanURL, "/live") {
						continue
					}
					parts := strings.Split(cleanURL, "/")
					if len(parts) > 0 {
						target := strings.ToLower(strings.TrimSuffix(parts[len(parts)-1], ".m3u8"))
						if target != "" {
							dedicatedStreamersMu.Lock()
							dedicatedStreamers[target] = true
							dedicatedStreamersMu.Unlock()
						}
					}
				}
			}
		}
		return
	}
}

func loadLapingvinoFollows() {
	paths := []string{
		filepath.Join(ProjectDir, "data", "lapingvino_follows.json"),
		filepath.Join(MediaDir, "data", "lapingvino_follows.json"),
		"/var/lib/iptv-live-bridge/data/lapingvino_follows.json",
		"/usr/share/iptv-live-bridge/data/lapingvino_follows.json",
		"/home/joop/iptv/data/lapingvino_follows.json",
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil || len(b) == 0 {
			continue
		}
		var items []FollowedStreamer
		if err := json.Unmarshal(b, &items); err == nil && len(items) > 0 {
			setFollows(items)
			log.Printf("[Twitch] Loaded %d followed streamers for lapingvino from %s", len(items), p)
			return
		}
	}

	if len(embeddedFollowsJSON) > 0 {
		var items []FollowedStreamer
		if err := json.Unmarshal([]byte(embeddedFollowsJSON), &items); err == nil && len(items) > 0 {
			setFollows(items)
			log.Printf("[Twitch] Loaded %d followed streamers for lapingvino from embedded binary data", len(items))
		}
	}
}

func setFollows(items []FollowedStreamer) {
	lapingvinoFollowsMu.Lock()
	defer lapingvinoFollowsMu.Unlock()
	lapingvinoFollowsList = make([]string, 0, len(items))
	lapingvinoFollowsSet = make(map[string]bool, len(items))
	for _, item := range items {
		l := strings.ToLower(item.BroadcasterLogin)
		if l != "" {
			lapingvinoFollowsList = append(lapingvinoFollowsList, l)
			lapingvinoFollowsSet[l] = true
		}
	}
}

func isLapingvinoFollow(login string) bool {
	lapingvinoFollowsMu.RLock()
	defer lapingvinoFollowsMu.RUnlock()
	return lapingvinoFollowsSet[strings.ToLower(login)]
}

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

// Essential Fallbacks for multi-channel events and 24/7 non-game streams
var creatorCircles = map[string][]string{
	// CTWC Multi-Feeds (mirror to main championship channel)
	"classictetris2": {"classictetris"},
	"classictetris3": {"classictetris"},
	"classictetris4": {"classictetris"},

	// Speedrun Marathon & Archive Relays
	"tasvideos":        {"speedrun", "gamesdonequick"},
	"esamarathon":      {"gamesdonequick", "speedrun"},
	"gamesdonequick":   {"speedrun", "esamarathon"},
	"worldoflongplays": {"speedrun"},

	// Retro, Culture & 24/7 Music/Slow TV
	"mst3k":            {"shoutfactorytv"},
	"shoutfactorytv":   {"mst3k"},
	"bobross":          {"lofigirl", "relaxbeats"},
	"lofigirl":         {"relaxbeats", "leekbeats", "monstercat"},
	"relaxbeats":       {"lofigirl", "leekbeats", "monstercat"},
	"leekbeats":        {"lofigirl", "relaxbeats", "monstercat"},
	"monstercat":       {"insomniac", "lofigirl"},
	"insomniac":         {"monstercat", "relaxbeats"},

	// Game Categories Safety Nets (for when a game has 0 live streams on Twitch)
	"nes-tetris":                {"classictetris", "dogplayingtetris", "bluescuti", "speedrun"},
	"tetris":                    {"classictetris", "dogplayingtetris", "bluescuti", "speedrun"},
	"modern-tetris":             {"harddrop", "wumbotize", "doremy", "classictetris", "speedrun"},
	"super mario world":         {"speedrun"},
	"romhack-super mario world": {"speedrun"},
	"super mario maker 2":       {"speedrun"},
	"celeste":                   {"speedrun"},
	"portal":                    {"speedrun"},
	"portal 2":                  {"speedrun"},
}

// Known AFK / desktop / fake stream traps to strictly filter out
var blacklistedStreamers = map[string]bool{
	"hercules_lostdays": true,
}

// Non-game generic categories that should NOT trigger automatic game category fallback
var ignoredGameCategories = map[string]bool{
	"asmr":                          true,
	"just chatting":                 true,
	"pools, hot tubs, and beaches": true,
	"talk shows & podcasts":         true,
	"special events":                true,
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

	// 1. LAZY EVALUATION: Try primary channel directly FIRST with zero overhead!
	// In the vast majority of cases, the requested channel is live.
	// We avoid all GQL/fallback overhead unless this initial lookup fails.
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

	// 2. Only if primary channel is offline or errored, lazily fetch fallback signals
	info, _ := tm.fetchChannelFallbackInfo(ctx, channel)

	if info != nil && info.RaidTarget != "" {
		tm.mu.Lock()
		tm.raidMemories[channel] = RaidMemory{
			target:    info.RaidTarget,
			expiresAt: time.Now().Add(2 * time.Hour),
		}
		tm.mu.Unlock()
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

	// 4. Check Live Teammates (Primary Team from API) - prioritizing followed teammates
	if info != nil && len(info.Teammates) > 0 {
		for _, teammate := range info.Teammates {
			if isLapingvinoFollow(teammate) {
				if teammateURL, err := tm.resolveSingle(ctx, teammate); err == nil {
					log.Printf("[Twitch] %s offline -> routed to followed teammate %s (%s)", channel, teammate, info.TeamName)
					return teammateURL, nil
				}
			}
		}
		for _, teammate := range info.Teammates {
			if teammateURL, err := tm.resolveSingle(ctx, teammate); err == nil {
				log.Printf("[Twitch] %s offline -> routed to live teammate %s (%s)", channel, teammate, info.TeamName)
				return teammateURL, nil
			}
		}
	}

	// 5. Automatic Dynamic Fallback: Check if any followed streamer is live playing the same game (API + followed list)
	if info != nil && info.LastGameName != "" {
		cleanGame := strings.ToLower(strings.TrimSpace(info.LastGameName))
		if !ignoredGameCategories[cleanGame] && cleanGame != "games + demos" {
			liveFollows := tm.GetRankedLiveFollows(ctx)
			for _, f := range liveFollows {
				if f.Login != channel && strings.EqualFold(f.Game, info.LastGameName) {
					if fURL, err := tm.resolveSingle(ctx, f.Login); err == nil {
						log.Printf("[Twitch] %s offline -> automatically routed to followed streamer %s playing same game '%s'", channel, f.Login, f.Game)
						return fURL, nil
					}
				}
			}
		}
	}

	// 6. Minimal Essential Mirrors (for multi-channel events and 24/7 non-game archives)
	if circle, exists := creatorCircles[channel]; exists {
		for _, fb := range circle {
			if fbURL, fbErr := tm.resolveSingle(ctx, fb); fbErr == nil {
				log.Printf("[Twitch] %s offline -> routed to essential circle fallback %s", channel, fb)
				return fbURL, nil
			}
		}
	}

	// 7. Contextual Last Broadcast Game Category (broad Twitch search with personal follow bias)
	if info != nil && info.LastGameName != "" {
		cleanGame := strings.ToLower(strings.TrimSpace(info.LastGameName))
		if !ignoredGameCategories[cleanGame] && cleanGame != "games + demos" {
			gameURL, err := tm.ResolveGame(ctx, info.LastGameName, "")
			if err == nil && gameURL != "" {
				log.Printf("[Twitch] %s offline -> routed to top streamer in last played game '%s'", channel, info.LastGameName)
				return gameURL, nil
			}
		}
	}

	// 8. Tiered Last Resort: Check any live streamer from lapingvino's followed channels!
	if fallbackURL, fallbackLogin := tm.resolveLapingvinoFollowedLastResort(ctx); fallbackURL != "" {
		log.Printf("[Twitch] %s exhausted all fallbacks -> routed to lapingvino followed last-resort '%s'", channel, fallbackLogin)
		return fallbackURL, nil
	}

	return "", fmt.Errorf("channel %s and all fallbacks offline", channel)
}

func (tm *TwitchManager) resolveLapingvinoFollowedLastResort(ctx context.Context) (string, string) {
	liveList := tm.GetRankedLiveFollows(ctx)
	if len(liveList) == 0 {
		return "", ""
	}

	// 1. Prefer gaming streamers (exclude non-game categories like Just Chatting, ASMR, etc.)
	for _, s := range liveList {
		g := strings.ToLower(strings.TrimSpace(s.Game))
		if !ignoredGameCategories[g] && g != "" && g != "unknown" {
			if streamURL, err := tm.resolveSingle(ctx, s.Login); err == nil {
				return streamURL, s.Login
			}
		}
	}

	// 2. Fallback to any live streamer
	for _, s := range liveList {
		if streamURL, err := tm.resolveSingle(ctx, s.Login); err == nil {
			return streamURL, s.Login
		}
	}

	return "", ""
}

type LiveStreamerInfo struct {
	Login       string
	DisplayName string
	Game        string
	Viewers     int
	Title       string
}

var (
	rankedFollowsMu         sync.RWMutex
	rankedFollowsList       []LiveStreamerInfo
	rankedFollowsTime       time.Time
	rankedFollowsRefreshing bool
)

func (tm *TwitchManager) GetRankedLiveFollows(ctx context.Context) []LiveStreamerInfo {
	rankedFollowsMu.RLock()
	cached := rankedFollowsList
	isStale := time.Since(rankedFollowsTime) >= 90*time.Second
	isRefreshing := rankedFollowsRefreshing
	rankedFollowsMu.RUnlock()

	// If we have cached results, return them immediately (lazy non-blocking)!
	if len(cached) > 0 {
		if isStale && !isRefreshing {
			rankedFollowsMu.Lock()
			if !rankedFollowsRefreshing {
				rankedFollowsRefreshing = true
				go func() {
					defer func() {
						rankedFollowsMu.Lock()
						rankedFollowsRefreshing = false
						rankedFollowsMu.Unlock()
					}()
					bgCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					tm.fetchRankedLiveFollows(bgCtx)
				}()
			}
			rankedFollowsMu.Unlock()
		}
		list := make([]LiveStreamerInfo, len(cached))
		copy(list, cached)
		return list
	}

	// Cold start (no cache yet): fetch synchronously
	return tm.fetchRankedLiveFollows(ctx)
}

func (tm *TwitchManager) fetchRankedLiveFollows(ctx context.Context) []LiveStreamerInfo {
	lapingvinoFollowsMu.RLock()
	logins := make([]string, len(lapingvinoFollowsList))
	copy(logins, lapingvinoFollowsList)
	lapingvinoFollowsMu.RUnlock()

	if len(logins) == 0 {
		return nil
	}

	chunkSize := 50
	var chunks [][]string
	for i := 0; i < len(logins); i += chunkSize {
		end := i + chunkSize
		if end > len(logins) {
			end = len(logins)
		}
		chunks = append(chunks, logins[i:end])
	}

	var resultsMu sync.Mutex
	var results []LiveStreamerInfo
	var wg sync.WaitGroup

	// Fetch chunks concurrently in parallel (drastically lowers latency from ~2.5s to ~300ms)
	for _, chunk := range chunks {
		wg.Add(1)
		go func(c []string) {
			defer wg.Done()
			var subqueries []string
			for _, l := range c {
				alias := "u_" + sanitizeAlias(l)
				subqueries = append(subqueries, fmt.Sprintf(`
				%s: user(login: "%s") {
					login
					displayName
					stream {
						title
						viewersCount
						game { name }
					}
				}`, alias, l))
			}
			query := "query GetFollowedLive {\n" + strings.Join(subqueries, "\n") + "\n}"
			payload, _ := json.Marshal(map[string]string{"query": query})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://gql.twitch.tv/gql", bytes.NewReader(payload))
			if err != nil {
				return
			}
			req.Header.Set("Client-Id", "kimne78kx3ncx6brgo4mv6wki5h1ko")
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var gData struct {
				Data map[string]*struct {
					Login       string `json:"login"`
					DisplayName string `json:"displayName"`
					Stream      *struct {
						Title        string `json:"title"`
						ViewersCount int    `json:"viewersCount"`
						Game         *struct {
							Name string `json:"name"`
						} `json:"game"`
					} `json:"stream"`
				} `json:"data"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&gData); err != nil || gData.Data == nil {
				return
			}

			var chunkResults []LiveStreamerInfo
			for _, l := range c {
				alias := "u_" + sanitizeAlias(l)
				if u, ok := gData.Data[alias]; ok && u != nil && u.Stream != nil {
					if isDedicatedStreamer(u.Login) {
						continue
					}
					gName := "Gaming"
					if u.Stream.Game != nil && u.Stream.Game.Name != "" {
						gName = u.Stream.Game.Name
					}
					chunkResults = append(chunkResults, LiveStreamerInfo{
						Login:       u.Login,
						DisplayName: u.DisplayName,
						Game:        gName,
						Viewers:     u.Stream.ViewersCount,
						Title:       u.Stream.Title,
					})
				}
			}

			if len(chunkResults) > 0 {
				resultsMu.Lock()
				results = append(results, chunkResults...)
				resultsMu.Unlock()
			}
		}(chunk)
	}

	wg.Wait()

	// Sort descending by viewer count
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Viewers > results[i].Viewers {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	rankedFollowsMu.Lock()
	rankedFollowsList = results
	rankedFollowsTime = time.Now()
	rankedFollowsMu.Unlock()

	return results
}

func (tm *TwitchManager) ResolveFollowedRank(ctx context.Context, rank int) (string, error) {
	if rank < 1 {
		rank = 1
	}
	liveList := tm.GetRankedLiveFollows(ctx)
	idx := rank - 1
	if idx < len(liveList) {
		target := liveList[idx].Login
		log.Printf("[Twitch] Followed Rank #%d -> resolving %s (%d viewers)", rank, target, liveList[idx].Viewers)
		if streamURL, err := tm.resolveSingle(ctx, target); err == nil {
			return streamURL, nil
		}
	}

	// If fewer streamers are live than rank requested or target failed, try other live candidates
	for _, s := range liveList {
		if streamURL, err := tm.resolveSingle(ctx, s.Login); err == nil {
			return streamURL, nil
		}
	}
	return tm.resolveSingle(ctx, "speedrun")
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
		circleKey := strings.ToLower(cleanName)
		if bias != "" {
			if circle, ok := creatorCircles[bias+"-"+circleKey]; ok {
				for _, fb := range circle {
					if fbURL, err := tm.resolveSingle(ctx, fb); err == nil {
						log.Printf("[Twitch] Game '%s' (bias=%s) had 0 streams -> routed to circle fallback '%s'", cleanName, bias, fb)
						return fbURL, nil
					}
				}
			}
		}
		if circle, ok := creatorCircles[circleKey]; ok {
			for _, fb := range circle {
				if fbURL, err := tm.resolveSingle(ctx, fb); err == nil {
					log.Printf("[Twitch] Game '%s' had 0 streams -> routed to circle fallback '%s'", cleanName, fb)
					return fbURL, nil
				}
			}
		}
		return "", fmt.Errorf("no live streams found for game %s", cleanName)
	}

	var topLogin string
	maxViewers := 0

	// Priority 1: Bias towards streamers followed by lapingvino for this game category!
	for _, e := range edges {
		login := strings.ToLower(e.Node.Broadcaster.Login)
		if blacklistedStreamers[login] {
			continue
		}
		if isLapingvinoFollow(login) {
			log.Printf("[Twitch] Game '%s' -> prioritizing lapingvino followed streamer '%s' (%d viewers)", cleanName, login, e.Node.ViewersCount)
			topLogin = e.Node.Broadcaster.Login
			break
		}
	}

	// Priority 2: Check keyword bias (romhack or nes)
	if topLogin == "" && (bias == "romhack" || bias == "nes") {
		for _, e := range edges {
			n := e.Node
			login := strings.ToLower(n.Broadcaster.Login)
			if blacklistedStreamers[login] {
				continue
			}
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
			login := strings.ToLower(e.Node.Broadcaster.Login)
			if blacklistedStreamers[login] {
				continue
			}
			if e.Node.ViewersCount >= 3 {
				topLogin = e.Node.Broadcaster.Login
				break
			}
		}
	}

	if topLogin == "" {
		for _, e := range edges {
			login := strings.ToLower(e.Node.Broadcaster.Login)
			if !blacklistedStreamers[login] {
				topLogin = e.Node.Broadcaster.Login
				break
			}
		}
	}

	if topLogin == "" {
		circleKey := strings.ToLower(cleanName)
		if bias != "" {
			if circle, ok := creatorCircles[bias+"-"+circleKey]; ok {
				for _, fb := range circle {
					if fbURL, err := tm.resolveSingle(ctx, fb); err == nil {
						log.Printf("[Twitch] Game '%s' (bias=%s) had no suitable/unblacklisted streamers -> routed to circle fallback '%s'", cleanName, bias, fb)
						return fbURL, nil
					}
				}
			}
		}
		if circle, ok := creatorCircles[circleKey]; ok {
			for _, fb := range circle {
				if fbURL, err := tm.resolveSingle(ctx, fb); err == nil {
					log.Printf("[Twitch] Game '%s' had no suitable/unblacklisted streamers -> routed to circle fallback '%s'", cleanName, fb)
					return fbURL, nil
				}
			}
		}
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
