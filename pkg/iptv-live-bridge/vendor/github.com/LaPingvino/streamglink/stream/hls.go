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
	nameRegex  = regexp.MustCompile(`NAME="([^"]+)"`)
	videoRegex = regexp.MustCompile(`VIDEO="([^"]+)"`)
)

// ParseMasterPlaylist parses an HLS master playlist body and extracts stream variants.
func ParseMasterPlaylist(masterBody, masterURL string, headers map[string]string, client *http.Client) (map[string]Stream, error) {
	lines := strings.Split(masterBody, "\n")
	streams := make(map[string]Stream)

	var currentQuality string
	var firstStream Stream
	var lastStream Stream

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			if m := nameRegex.FindStringSubmatch(line); len(m) > 1 {
				currentQuality = cleanQuality(m[1])
			} else if m := videoRegex.FindStringSubmatch(line); len(m) > 1 {
				currentQuality = cleanQuality(m[1])
			} else {
				currentQuality = "video"
			}
		} else if strings.HasPrefix(line, "#EXT-X-MEDIA:TYPE=AUDIO") {
			if m := nameRegex.FindStringSubmatch(line); len(m) > 1 {
				currentQuality = "audio_only"
			}
		} else if line != "" && !strings.HasPrefix(line, "#") {
			variantURL := line
			quality := currentQuality
			if quality == "" {
				quality = "live"
			}

			st := NewHLSStream(quality, variantURL, masterURL, headers, client)
			streams[quality] = st

			if firstStream == nil {
				firstStream = st
			}
			lastStream = st
			currentQuality = ""
		}
	}

	if len(streams) == 0 {
		return nil, errors.New("no stream variants found in master playlist")
	}

	if firstStream != nil {
		streams["best"] = firstStream
	}
	if lastStream != nil {
		streams["worst"] = lastStream
	}

	return streams, nil
}

func cleanQuality(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " (source)", "")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}
