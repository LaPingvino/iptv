package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	uriAttrRegex = regexp.MustCompile(`URI="([^"]+)"`)

	proxyHTTPClient = &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		},
	}
)

type ProxiedChannel struct {
	ID          string
	Name        string
	UpstreamURL string
	Logo        string
	TvgID       string
}

var ProxiedChannels = map[string]ProxiedChannel{
	"arte_fr": {
		ID:          "arte_fr",
		Name:        "ARTE France",
		UpstreamURL: "https://artesimulcast.akamaized.net/hls/live/2031003/artelive_fr/index.m3u8",
		Logo:        "https://api-cdn.arte.tv/img/v2/image/j4EoSJjX26uySVWdmt3EmS/1920x1080",
		TvgID:       "arte.fr@SD",
	},
	"arte_de": {
		ID:          "arte_de",
		Name:        "ARTE Deutschland",
		UpstreamURL: "https://artesimulcast.akamaized.net/hls/live/2030993/artelive_de/index.m3u8",
		Logo:        "https://api-cdn.arte.tv/img/v2/image/j4EoSJjX26uySVWdmt3EmS/1920x1080",
		TvgID:       "arte.de@SD",
	},
	"tv5monde_europe": {
		ID:          "tv5monde_europe",
		Name:        "TV5Monde Europe",
		UpstreamURL: "https://ott.tv5monde.com/Content/HLS/Live/channel(europe)/index.m3u8",
		Logo:        "https://i.imgur.com/uPmwTo9.png",
		TvgID:       "TV5MondeEurope.fr",
	},
	"tv5monde_info": {
		ID:          "tv5monde_info",
		Name:        "TV5Monde Info",
		UpstreamURL: "https://ott.tv5monde.com/Content/HLS/Live/channel(info)/index.m3u8",
		Logo:        "https://i.imgur.com/NcysrWH.png",
		TvgID:       "TV5MondeInfo.fr@SD",
	},
	"zdf": {
		ID:          "zdf",
		Name:        "ZDF",
		UpstreamURL: "https://zdf-hls-15.akamaized.net/hls/live/2016498/de/high/master.m3u8",
		Logo:        "https://i.imgur.com/rtLb6m9.png",
		TvgID:       "ZDF.de@SD",
	},
	"zdfneo": {
		ID:          "zdfneo",
		Name:        "ZDFneo",
		UpstreamURL: "https://zdf-hls-16.akamaized.net/hls/live/2016499/de/high/master.m3u8",
		Logo:        "https://i.imgur.com/1JIIGgL.png",
		TvgID:       "ZDFneo.de@SD",
	},
	"zdfinfo": {
		ID:          "zdfinfo",
		Name:        "ZDFinfo",
		UpstreamURL: "https://zdf-hls-17.akamaized.net/hls/live/2016500/de/high/master.m3u8",
		Logo:        "https://i.imgur.com/vtq1IWp.png",
		TvgID:       "ZDFinfo.de@SD",
	},
	"3sat": {
		ID:          "3sat",
		Name:        "3sat",
		UpstreamURL: "https://zdf-hls-18.akamaized.net/hls/live/2016501/dach/high/master.m3u8",
		Logo:        "https://upload.wikimedia.org/wikipedia/commons/thumb/8/81/3sat_2019.svg/960px-3sat_2019.svg.png",
		TvgID:       "3sat.de@SD",
	},
	"phoenix": {
		ID:          "phoenix",
		Name:        "Phoenix",
		UpstreamURL: "https://zdf-hls-19.akamaized.net/hls/live/2016502/de/high/master.m3u8",
		Logo:        "https://i.imgur.com/T1532t9.png",
		TvgID:       "phoenix.de@HD",
	},
	"kika": {
		ID:          "kika",
		Name:        "KiKa",
		UpstreamURL: "https://kikahls.akamaized.net/hls/live/2022690/livetvkika_ww/master.m3u8",
		Logo:        "https://i.imgur.com/vZdNp5S.png",
		TvgID:       "KiKA.de@SD",
	},
	"tagesschau24": {
		ID:          "tagesschau24",
		Name:        "Tagesschau 24",
		UpstreamURL: "https://tagesschau.akamaized.net/hls/live/2020115/tagesschau/tagesschau_1/master.m3u8",
		Logo:        "https://i.imgur.com/LH0Gctv.png",
		TvgID:       "tagesschau24.de@SD",
	},
	"wdr": {
		ID:          "wdr",
		Name:        "WDR Fernsehen",
		UpstreamURL: "https://wdr-live.ard-mcdn.de/wdr/live/hls/int/master.m3u8",
		Logo:        "https://upload.wikimedia.org/wikipedia/commons/thumb/2/22/WDR_Fernsehen_Logo_2018.svg/960px-WDR_Fernsehen_Logo_2018.svg.png",
		TvgID:       "WDRFernsehen.de@Koln",
	},
}

// ResolveProxiedChannel maps incoming path requests to a known ProxiedChannel.
func ResolveProxiedChannel(path string) (ProxiedChannel, bool) {
	norm := strings.ToLower(strings.Trim(path, "/"))
	norm = strings.TrimPrefix(norm, "iptv/")
	norm = strings.TrimPrefix(norm, "live/")

	// Direct alias checks
	switch norm {
	case "arte", "arte.m3u8", "arte_fr", "arte_fr.m3u8", "arte/fr", "arte/fr.m3u8":
		return ProxiedChannels["arte_fr"], true
	case "arte_de", "arte_de.m3u8", "arte/de", "arte/de.m3u8":
		return ProxiedChannels["arte_de"], true
	case "tv5monde", "tv5monde.m3u8", "tv5monde_europe", "tv5monde_europe.m3u8", "tv5", "tv5.m3u8":
		return ProxiedChannels["tv5monde_europe"], true
	case "tv5monde_info", "tv5monde_info.m3u8", "tv5info", "tv5info.m3u8":
		return ProxiedChannels["tv5monde_info"], true
	case "zdf", "zdf.m3u8":
		return ProxiedChannels["zdf"], true
	case "zdfneo", "zdfneo.m3u8":
		return ProxiedChannels["zdfneo"], true
	case "zdfinfo", "zdfinfo.m3u8":
		return ProxiedChannels["zdfinfo"], true
	case "3sat", "3sat.m3u8":
		return ProxiedChannels["3sat"], true
	case "phoenix", "phoenix.m3u8":
		return ProxiedChannels["phoenix"], true
	case "kika", "kika.m3u8":
		return ProxiedChannels["kika"], true
	case "tagesschau24", "tagesschau24.m3u8", "tagesschau", "tagesschau.m3u8":
		return ProxiedChannels["tagesschau24"], true
	case "wdr", "wdr.m3u8":
		return ProxiedChannels["wdr"], true
	}

	trimmedExt := strings.TrimSuffix(norm, ".m3u8")
	if ch, ok := ProxiedChannels[trimmedExt]; ok {
		return ch, true
	}

	return ProxiedChannel{}, false
}

// RewriteM3U8 parses an HLS manifest and rewrites all child playlists and media segments
// into root-relative URLs routed through our Go live bridge.
func RewriteM3U8(content string, upstreamURL string) (string, error) {
	baseURL, err := url.Parse(upstreamURL)
	if err != nil {
		return "", err
	}

	lines := strings.Split(content, "\n")
	var out []string
	expectVariantURI := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			out = append(out, line)
			continue
		}

		if strings.HasPrefix(trimmed, "#EXT-X-STREAM-INF:") {
			expectVariantURI = true
			out = append(out, line)
			continue
		}

		if strings.HasPrefix(trimmed, "#EXT-X-I-FRAME-STREAM-INF:") {
			rewrittenLine := uriAttrRegex.ReplaceAllStringFunc(line, func(m string) string {
				parts := uriAttrRegex.FindStringSubmatch(m)
				if len(parts) < 2 {
					return m
				}
				refURL, err := url.Parse(parts[1])
				if err != nil {
					return m
				}
				resolved := baseURL.ResolveReference(refURL).String()
				token := base64.RawURLEncoding.EncodeToString([]byte(resolved))
				return fmt.Sprintf(`URI="/iptv/hls/m/%s/playlist.m3u8"`, token)
			})
			out = append(out, rewrittenLine)
			continue
		}

		if strings.HasPrefix(trimmed, "#EXT-X-MEDIA:") {
			rewrittenLine := uriAttrRegex.ReplaceAllStringFunc(line, func(m string) string {
				parts := uriAttrRegex.FindStringSubmatch(m)
				if len(parts) < 2 {
					return m
				}
				refURL, err := url.Parse(parts[1])
				if err != nil {
					return m
				}
				resolved := baseURL.ResolveReference(refURL).String()
				token := base64.RawURLEncoding.EncodeToString([]byte(resolved))
				return fmt.Sprintf(`URI="/iptv/hls/m/%s/playlist.m3u8"`, token)
			})
			out = append(out, rewrittenLine)
			continue
		}

		if strings.HasPrefix(trimmed, "#EXT-X-MAP:") {
			rewrittenLine := uriAttrRegex.ReplaceAllStringFunc(line, func(m string) string {
				parts := uriAttrRegex.FindStringSubmatch(m)
				if len(parts) < 2 {
					return m
				}
				refURL, err := url.Parse(parts[1])
				if err != nil {
					return m
				}
				resolved := baseURL.ResolveReference(refURL).String()
				token := base64.RawURLEncoding.EncodeToString([]byte(resolved))
				ext := "segment.mp4"
				if strings.Contains(resolved, ".m4s") {
					ext = "segment.m4s"
				}
				return fmt.Sprintf(`URI="/iptv/hls/s/%s/%s"`, token, ext)
			})
			out = append(out, rewrittenLine)
			continue
		}

		if strings.HasPrefix(trimmed, "#EXT-X-KEY:") {
			rewrittenLine := uriAttrRegex.ReplaceAllStringFunc(line, func(m string) string {
				parts := uriAttrRegex.FindStringSubmatch(m)
				if len(parts) < 2 {
					return m
				}
				refURL, err := url.Parse(parts[1])
				if err != nil {
					return m
				}
				resolved := baseURL.ResolveReference(refURL).String()
				token := base64.RawURLEncoding.EncodeToString([]byte(resolved))
				return fmt.Sprintf(`URI="/iptv/hls/s/%s/key.bin"`, token)
			})
			out = append(out, rewrittenLine)
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			out = append(out, line)
			continue
		}

		// Non-comment URI line: either a variant playlist or a media segment
		refURL, err := url.Parse(trimmed)
		if err != nil {
			out = append(out, line)
			continue
		}
		resolved := baseURL.ResolveReference(refURL).String()
		token := base64.RawURLEncoding.EncodeToString([]byte(resolved))

		if expectVariantURI || strings.HasSuffix(strings.ToLower(refURL.Path), ".m3u8") {
			expectVariantURI = false
			out = append(out, fmt.Sprintf("/iptv/hls/m/%s/playlist.m3u8", token))
		} else {
			ext := "segment.ts"
			lowerPath := strings.ToLower(refURL.Path)
			if strings.HasSuffix(lowerPath, ".m4s") {
				ext = "segment.m4s"
			} else if strings.HasSuffix(lowerPath, ".mp4") {
				ext = "segment.mp4"
			}
			out = append(out, fmt.Sprintf("/iptv/hls/s/%s/%s", token, ext))
		}
	}

	return strings.Join(out, "\n"), nil
}

// HandleProxiedChannel serves the root master playlist for a named channel.
func HandleProxiedChannel(w http.ResponseWriter, r *http.Request, ch ProxiedChannel) {
	token := base64.RawURLEncoding.EncodeToString([]byte(ch.UpstreamURL))
	HandleHLSManifest(w, r, token)
}

// HandleHLSManifest fetches and rewrites an upstream HLS playlist.
func HandleHLSManifest(w http.ResponseWriter, r *http.Request, rawToken string) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.WriteHeader(http.StatusOK)
		return
	}

	manifestURLBytes, err := base64.RawURLEncoding.DecodeString(rawToken)
	if err != nil {
		http.Error(w, "invalid manifest token", http.StatusBadRequest)
		return
	}
	manifestURL := string(manifestURLBytes)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, manifestURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")

	resp, err := proxyHTTPClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode), resp.StatusCode)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	finalURL := manifestURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	rewritten, err := RewriteM3U8(string(body), finalURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(rewritten))
}

// HandleHLSSegment fetches and streams an upstream media segment (.ts, .m4s, .mp4).
func HandleHLSSegment(w http.ResponseWriter, r *http.Request, rawToken string) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.WriteHeader(http.StatusOK)
		return
	}

	segURLBytes, err := base64.RawURLEncoding.DecodeString(rawToken)
	if err != nil {
		http.Error(w, "invalid segment token", http.StatusBadRequest)
		return
	}
	segURL := string(segURLBytes)

	req, err := http.NewRequestWithContext(r.Context(), r.Method, segURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")

	resp, err := proxyHTTPClient.Do(req)
	if err != nil {
		log.Printf("[HLS-Proxy] Upstream segment error: %v (url: %s)", err, segURL)
		http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if val := resp.Header.Get(h); val != "" {
			w.Header().Set(h, val)
		}
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Cache-Control", "public, max-age=3600")

	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}
