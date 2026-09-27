package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
)

// This file lets the Twitch EPG say what each channel actually plays right now.
// decideUser/decideGame mirror the fallback order of TwitchManager.Resolve and
// ResolveGame (twitch.go) using batched GQL liveness instead of opening streams.
// Keep them in sync when that order changes.

type gqlStream struct {
	Title        string `json:"title"`
	ViewersCount int    `json:"viewersCount"`
	Game         *struct {
		Name string `json:"name"`
	} `json:"game"`
}

func (s *gqlStream) gameName() string {
	if s != nil && s.Game != nil && s.Game.Name != "" {
		return s.Game.Name
	}
	return "Gaming"
}

type gqlUser struct {
	Login       string     `json:"login"`
	DisplayName string     `json:"displayName"`
	Stream      *gqlStream `json:"stream"`
	Raid        *struct {
		TargetChannel *struct {
			Login       string `json:"login"`
			DisplayName string `json:"displayName"`
		} `json:"targetChannel"`
	} `json:"raid"`
	Hosting *struct {
		Login  string     `json:"login"`
		Stream *gqlStream `json:"stream"`
	} `json:"hosting"`
	PrimaryTeam *struct {
		DisplayName string `json:"displayName"`
		Members     struct {
			Edges []struct {
				Node struct {
					Login       string     `json:"login"`
					DisplayName string     `json:"displayName"`
					Stream      *gqlStream `json:"stream"`
				} `json:"node"`
			} `json:"edges"`
		} `json:"members"`
	} `json:"primaryTeam"`
	LastBroadcast *struct {
		Title string `json:"title"`
		Game  *struct {
			Name string `json:"name"`
		} `json:"game"`
	} `json:"lastBroadcast"`
}

func (u *gqlUser) name(fallback string) string {
	if u != nil && u.DisplayName != "" {
		return u.DisplayName
	}
	return fallback
}

func (u *gqlUser) lastGame() string {
	if u != nil && u.LastBroadcast != nil && u.LastBroadcast.Game != nil {
		return u.LastBroadcast.Game.Name
	}
	return ""
}

type gqlGameStream struct {
	Broadcaster struct {
		Login       string `json:"login"`
		DisplayName string `json:"displayName"`
	} `json:"broadcaster"`
	Title        string `json:"title"`
	ViewersCount int    `json:"viewersCount"`
}

type gqlGame struct {
	Name    string `json:"name"`
	Streams struct {
		Edges []struct {
			Node gqlGameStream `json:"node"`
		} `json:"edges"`
	} `json:"streams"`
}

const gqlUserFields = `
	login
	displayName
	stream { title viewersCount game { name } }
	raid { targetChannel { login displayName } }
	hosting { login stream { title viewersCount game { name } } }
	primaryTeam {
		displayName
		members { edges { node { login displayName stream { title viewersCount game { name } } } } }
	}
	lastBroadcast { title game { name } }`

func gqlUserQuery(alias, login string) string {
	return fmt.Sprintf("%s: user(login: %q) {%s\n}", alias, login, gqlUserFields)
}

func gqlGameQuery(alias, game string) string {
	return fmt.Sprintf(`%s: game(name: %q) {
	name
	streams(first: 10) { edges { node { broadcaster { login displayName } title viewersCount } } }
}`, alias, html.UnescapeString(game))
}

// twitchGQL runs one batched GQL query built from aliased sub-queries.
func twitchGQL(ctx context.Context, queries []string) (map[string]json.RawMessage, error) {
	if len(queries) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	payload, _ := json.Marshal(map[string]string{"query": "query BatchTwitchEPG {\n" + strings.Join(queries, "\n") + "\n}"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://gql.twitch.tv/gql", bytes.NewReader(payload))
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("twitch GQL returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// pickGameStream picks like ResolveGame: a followed streamer first, then the
// first non-blacklisted stream with >=3 viewers, then any non-blacklisted one.
func pickGameStream(g *gqlGame) *gqlGameStream {
	if g == nil {
		return nil
	}
	edges := g.Streams.Edges
	for i := range edges {
		l := strings.ToLower(edges[i].Node.Broadcaster.Login)
		if !blacklistedStreamers[l] && isLapingvinoFollow(l) {
			return &edges[i].Node
		}
	}
	for pass := 0; pass < 2; pass++ {
		for i := range edges {
			if blacklistedStreamers[strings.ToLower(edges[i].Node.Broadcaster.Login)] {
				continue
			}
			if pass == 1 || edges[i].Node.ViewersCount >= 3 {
				return &edges[i].Node
			}
		}
	}
	return nil
}

// nowDecision is what a channel shows right now and why.
type nowDecision struct {
	State   string // live, raid, host, team, relay, circle, lastgame, lastresort, offline
	Who     string
	Game    string
	Title   string
	Viewers int
	Note    string // short reason, e.g. "raid", "equipa X", "espelho"
}

// liveUser returns a live decision for login if the batch saw it streaming.
func liveUser(users map[string]*gqlUser, login string) (nowDecision, bool) {
	u := users[strings.ToLower(login)]
	if u == nil || u.Stream == nil {
		return nowDecision{}, false
	}
	return nowDecision{Who: u.name(login), Game: u.Stream.gameName(), Title: u.Stream.Title, Viewers: u.Stream.ViewersCount}, true
}

// decideUser mirrors TwitchManager.Resolve for a channel login.
func decideUser(login string, users map[string]*gqlUser, gameTop map[string]*gqlGameStream, liveFollows []LiveStreamerInfo) nowDecision {
	u := users[strings.ToLower(login)]
	if d, ok := liveUser(users, login); ok {
		d.State = "live"
		return d
	}
	if u == nil {
		return nowDecision{State: "offline"}
	}
	withState := func(d nowDecision, state, note string) nowDecision {
		d.State, d.Note = state, note
		return d
	}
	// 2. raid target
	if u.Raid != nil && u.Raid.TargetChannel != nil && u.Raid.TargetChannel.Login != "" {
		t := u.Raid.TargetChannel
		if d, ok := liveUser(users, t.Login); ok {
			return withState(d, "raid", "raid")
		}
		return nowDecision{State: "raid", Who: t.DisplayName, Note: "raid"}
	}
	// 3. host target
	if u.Hosting != nil && u.Hosting.Stream != nil && u.Hosting.Login != "" {
		s := u.Hosting.Stream
		return nowDecision{State: "host", Who: u.Hosting.Login, Game: s.gameName(), Title: s.Title, Viewers: s.ViewersCount, Note: "host"}
	}
	// 4. live teammates, followed ones first, then by viewers
	if u.PrimaryTeam != nil {
		var best *nowDecision
		bestFollowed := false
		for _, e := range u.PrimaryTeam.Members.Edges {
			n := e.Node
			if n.Stream == nil || strings.EqualFold(n.Login, login) {
				continue
			}
			followed := isLapingvinoFollow(strings.ToLower(n.Login))
			if best == nil || (followed && !bestFollowed) || (followed == bestFollowed && n.Stream.ViewersCount > best.Viewers) {
				d := nowDecision{State: "team", Who: n.DisplayName, Game: n.Stream.gameName(), Title: n.Stream.Title, Viewers: n.Stream.ViewersCount, Note: "equipa " + u.PrimaryTeam.DisplayName}
				best, bestFollowed = &d, followed
			}
		}
		if best != nil {
			return *best
		}
	}
	last := u.lastGame()
	cleanLast := strings.ToLower(strings.TrimSpace(last))
	gameOK := last != "" && !ignoredGameCategories[cleanLast] && cleanLast != "games + demos"
	// 5. followed streamer live in the same game
	if gameOK {
		for _, f := range liveFollows {
			if !strings.EqualFold(f.Login, login) && strings.EqualFold(f.Game, last) {
				return nowDecision{State: "relay", Who: f.DisplayName, Game: f.Game, Title: f.Title, Viewers: f.Viewers, Note: "mesmo jogo"}
			}
		}
	}
	// 6. essential mirror circle: first live member
	for _, fb := range creatorCircles[strings.ToLower(login)] {
		if d, ok := liveUser(users, fb); ok {
			return withState(d, "circle", "espelho")
		}
	}
	// 7. top streamer in the last played game
	if gameOK {
		if s := gameTop[strings.ToLower(last)]; s != nil {
			return nowDecision{State: "lastgame", Who: s.Broadcaster.DisplayName, Game: last, Title: s.Title, Viewers: s.ViewersCount, Note: "último jogo"}
		}
		// ResolveGame found no suitable stream: it tries the game's circle next.
		for _, fb := range creatorCircles[strings.ToLower(last)] {
			if d, ok := liveUser(users, fb); ok {
				d.State, d.Note = "lastgame", "último jogo · espelho"
				return d
			}
		}
	}
	// 8. last resort: any live follow, gaming categories first
	for pass := 0; pass < 2; pass++ {
		for _, f := range liveFollows {
			g := strings.ToLower(strings.TrimSpace(f.Game))
			if pass == 1 || (!ignoredGameCategories[g] && g != "" && g != "unknown") {
				return nowDecision{State: "lastresort", Who: f.DisplayName, Game: f.Game, Title: f.Title, Viewers: f.Viewers, Note: "seguido"}
			}
		}
	}
	return nowDecision{State: "offline", Game: last}
}

// decideGame mirrors ResolveGame for a game channel (bias from ?bias=...).
func decideGame(ch EPGChannelDef, g *gqlGame, users map[string]*gqlUser) nowDecision {
	if s := pickGameStream(g); s != nil {
		return nowDecision{State: "live", Who: s.Broadcaster.DisplayName, Game: ch.GameName, Title: s.Title, Viewers: s.ViewersCount}
	}
	for _, fb := range gameCircleMembers(ch) {
		if d, ok := liveUser(users, fb); ok {
			d.State, d.Note = "circle", "espelho"
			return d
		}
	}
	return nowDecision{State: "offline", Game: ch.GameName}
}
