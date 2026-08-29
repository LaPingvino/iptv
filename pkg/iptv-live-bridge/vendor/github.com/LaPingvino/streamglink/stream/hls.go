package stream

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// HLSStream represents an Apple HTTP Live Streaming (HLS) variant.
type HLSStream struct {
	BaseStream
	MasterURL string
}

func NewHLSStream(quality, variantURL, masterURL string, headers map[string]string, client *http.Client) *HLSStream {
	return &HLSStream{
		BaseStream: BaseStream{
			StreamQuality: quality,
			StreamURL:     variantURL,
			StreamHeaders: headers,
			Client:        client,
		},
		MasterURL: masterURL,
	}
}

func (s *HLSStream) String() string {
	return fmt.Sprintf("<HLSStream [%s] %s>", s.StreamQuality, s.StreamURL)
}

var (
	nameRegex      = regexp.MustCompile(`NAME="([^"]+)"`)
	videoRegex     = regexp.MustCompile(`VIDEO="([^"]+)"`)
	bandwidthRegex = regexp.MustCompile(`BANDWIDTH=(\d+)`)
	codecsRegex    = regexp.MustCompile(`CODECS="([^"]+)"`)
)

// ParseMasterPlaylist parses an HLS master playlist body and extracts stream variants.
func ParseMasterPlaylist(masterBody, masterURL string, headers map[string]string, client *http.Client) (map[string]Stream, error) {
	lines := strings.Split(masterBody, "\n")
	streams := make(map[string]Stream)

	var currentQuality string
	var currentBandwidth int
	var currentIsAudio bool

	type rankedStream struct {
		stream    Stream
		bandwidth int
		isAudio   bool
	}
	var allRanked []rankedStream

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			currentIsAudio = false
			if m := bandwidthRegex.FindStringSubmatch(line); len(m) > 1 {
				fmt.Sscanf(m[1], "%d", &currentBandwidth)
			}
			if m := codecsRegex.FindStringSubmatch(line); len(m) > 1 {
				codecs := strings.ToLower(m[1])
				// If codecs only declare audio (mp4a), treat as audio
				if strings.Contains(codecs, "mp4a") && !strings.Contains(codecs, "avc") && !strings.Contains(codecs, "hvc") && !strings.Contains(codecs, "av01") {
					currentIsAudio = true
				}
			}
			if m := nameRegex.FindStringSubmatch(line); len(m) > 1 {
				currentQuality = cleanQuality(m[1])
			} else if m := videoRegex.FindStringSubmatch(line); len(m) > 1 {
				currentQuality = cleanQuality(m[1])
			} else {
				currentQuality = "video"
			}
			if strings.Contains(currentQuality, "audio_only") {
				currentIsAudio = true
			}
		} else if strings.HasPrefix(line, "#EXT-X-MEDIA:TYPE=AUDIO") {
			currentIsAudio = true
			currentQuality = "audio_only"
		} else if line != "" && !strings.HasPrefix(line, "#") {
			variantURL := line
			quality := currentQuality
			if quality == "" {
				quality = "live"
			}

			st := NewHLSStream(quality, variantURL, masterURL, headers, client)
			streams[quality] = st
			allRanked = append(allRanked, rankedStream{
				stream:    st,
				bandwidth: currentBandwidth,
				isAudio:   currentIsAudio,
			})

			currentQuality = ""
			currentBandwidth = 0
			currentIsAudio = false
		}
	}

	if len(streams) == 0 {
		return nil, errors.New("no stream variants found in master playlist")
	}

	// Sort video streams by bandwidth to pick best and worst
	var bestVideo Stream
	var worstVideo Stream
	highestVideoBW := -1
	lowestVideoBW := 999999999

	for _, r := range allRanked {
		if !r.isAudio {
			if r.bandwidth > highestVideoBW {
				highestVideoBW = r.bandwidth
				bestVideo = r.stream
			}
			if r.bandwidth < lowestVideoBW {
				lowestVideoBW = r.bandwidth
				worstVideo = r.stream
			}
		}
	}

	// Fallback to first/last if no video tags were parsed
	if bestVideo != nil {
		streams["best"] = bestVideo
	} else if len(allRanked) > 0 {
		streams["best"] = allRanked[0].stream
	}

	if worstVideo != nil {
		streams["worst"] = worstVideo
	} else if len(allRanked) > 0 {
		streams["worst"] = allRanked[len(allRanked)-1].stream
	}

	return streams, nil
}

func cleanQuality(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " (source)", "")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}
