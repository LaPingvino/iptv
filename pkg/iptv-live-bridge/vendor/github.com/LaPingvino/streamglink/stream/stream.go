package stream

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// Stream is the base interface for all stream implementations in streamglink.
type Stream interface {
	Quality() string
	URL() string
	Headers() map[string]string
	Open(ctx context.Context) (io.ReadCloser, error)
	String() string
}

// BaseStream provides common functionality for all stream types.
type BaseStream struct {
	StreamQuality string
	StreamURL     string
	StreamHeaders map[string]string
	Client        *http.Client
}

func (s *BaseStream) Quality() string            { return s.StreamQuality }
func (s *BaseStream) URL() string                { return s.StreamURL }
func (s *BaseStream) Headers() map[string]string { return s.StreamHeaders }

func (s *BaseStream) Open(ctx context.Context) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.StreamURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range s.StreamHeaders {
		req.Header.Set(k, v)
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, resp.Status)
	}
	return resp.Body, nil
}

func (s *BaseStream) String() string {
	return fmt.Sprintf("<%s (%s)>", s.StreamQuality, s.StreamURL)
}
