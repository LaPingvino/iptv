package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/LaPingvino/streamglink"
	"github.com/LaPingvino/streamglink/plugin"
	"github.com/LaPingvino/streamglink/stream"
)

const (
	ClientID   = "kimne78kx3ncx6brgo4mv6wki5h1ko"
	GQLURL     = "https://gql.twitch.tv/gql"
	UsherURL   = "https://usher.ttvnw.net/api/channel/hls/%s.m3u8"
	Sha256Hash = "ed230aa1e33e07eebb8928504583da78a5173989fadfb1ac94be06a04f3cdbe9"
)

func init() {
	streamglink.RegisterPlugin(New())
}

type Plugin struct {
	plugin.BasePlugin
}

func New() *Plugin {
	return &Plugin{
		BasePlugin: plugin.BasePlugin{
			PluginName: "twitch",
			MatchList: []*plugin.Matcher{
				plugin.NewMatcher(`^https?://(?:(?:www|go|m)\.)?twitch\.tv/([a-zA-Z0-9_]+)`, plugin.PriorityNormal),
			},
		},
	}
}

type gqlPayload struct {
	OperationName string `json:"operationName"`
	Extensions    struct {
		PersistedQuery struct {
			Version    int    `json:"version"`
			Sha256Hash string `json:"sha256Hash"`
		} `json:"persistedQuery"`
	} `json:"extensions"`
	Variables struct {
		IsLive     bool   `json:"isLive"`
		Login      string `json:"login"`
		IsVod      bool   `json:"isVod"`
		VodID      string `json:"vodID"`
		PlayerType string `json:"playerType"`
		Platform   string `json:"platform"`
	} `json:"variables"`
}

type gqlResponse struct {
	Data struct {
		StreamPlaybackAccessToken struct {
			Value     string `json:"value"`
			Signature string `json:"signature"`
		} `json:"streamPlaybackAccessToken"`
	} `json:"data"`
}

func (p *Plugin) Streams(ctx context.Context, session plugin.SessionInterface, u *url.URL) (map[string]stream.Stream, error) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return nil, errors.New("missing channel name in Twitch URL")
	}
	channel := strings.ToLower(parts[0])

	// 1. Query GQL for playback access token
	val, sig, err := fetchToken(ctx, session, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to get twitch playback access token: %w", err)
	}

	// 2. Fetch Usher master playlist
	q := url.Values{}
	q.Set("client_id", ClientID)
	q.Set("token", val)
	q.Set("sig", sig)
	q.Set("allow_source", "true")
	q.Set("allow_audio_only", "true")
	q.Set("fast_bread", "true")
	q.Set("p", "987654")

	fullUsherURL := fmt.Sprintf(UsherURL, channel) + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullUsherURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", session.UserAgent())

	client := session.HTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usher request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usher HTTP %d (stream may be offline)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"User-Agent": session.UserAgent(),
	}

	// 3. Parse HLS master variants
	return stream.ParseMasterPlaylist(string(body), fullUsherURL, headers, client)
}

func fetchToken(ctx context.Context, session plugin.SessionInterface, channel string) (string, string, error) {
	var payload gqlPayload
	payload.OperationName = "PlaybackAccessToken"
	payload.Extensions.PersistedQuery.Version = 1
	payload.Extensions.PersistedQuery.Sha256Hash = Sha256Hash
	payload.Variables.IsLive = true
	payload.Variables.Login = channel
	payload.Variables.PlayerType = "embed"
	payload.Variables.Platform = "web"

	data, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GQLURL, bytes.NewReader(data))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Client-Id", ClientID)
	req.Header.Set("User-Agent", session.UserAgent())
	req.Header.Set("Content-Type", "application/json")

	resp, err := session.HTTPClient().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GQL error HTTP %d", resp.StatusCode)
	}

	var res gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", "", err
	}

	val := res.Data.StreamPlaybackAccessToken.Value
	sig := res.Data.StreamPlaybackAccessToken.Signature
	if val == "" || sig == "" {
		return "", "", errors.New("empty playback token (channel offline or does not exist)")
	}

	return val, sig, nil
}
