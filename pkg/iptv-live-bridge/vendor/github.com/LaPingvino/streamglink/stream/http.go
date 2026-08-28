package stream

import (
	"fmt"
	"net/http"
)

// HTTPStream represents a direct progressive HTTP or raw media stream.
type HTTPStream struct {
	BaseStream
}

func NewHTTPStream(quality, rawURL string, headers map[string]string, client *http.Client) *HTTPStream {
	return &HTTPStream{
		BaseStream: BaseStream{
			StreamQuality: quality,
			StreamURL:     rawURL,
			StreamHeaders: headers,
			Client:        client,
		},
	}
}

func (s *HTTPStream) String() string {
	return fmt.Sprintf("<HTTPStream [%s] %s>", s.StreamQuality, s.StreamURL)
}
