package streamglink

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/LaPingvino/streamglink/plugin"
	"github.com/LaPingvino/streamglink/stream"
)

var (
	registryMu sync.RWMutex
	plugins    []plugin.Plugin
)

// RegisterPlugin adds a plugin to the global Streamlink registry.
func RegisterPlugin(p plugin.Plugin) {
	registryMu.Lock()
	defer registryMu.Unlock()
	plugins = append(plugins, p)
}

// Streamlink is the main session controller, matching Streamlink's core class.
type Streamlink struct {
	client    *http.Client
	userAgent string
	optionsMu sync.RWMutex
	options   map[string]string
}

// New creates a new Streamlink session with production defaults.
func New() *Streamlink {
	return &Streamlink{
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		userAgent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
		options:   make(map[string]string),
	}
}

func (s *Streamlink) HTTPClient() *http.Client {
	return s.client
}

func (s *Streamlink) UserAgent() string {
	return s.userAgent
}

func (s *Streamlink) SetUserAgent(ua string) {
	s.userAgent = ua
}

func (s *Streamlink) GetOption(key string) (string, bool) {
	s.optionsMu.RLock()
	defer s.optionsMu.RUnlock()
	v, ok := s.options[key]
	return v, ok
}

func (s *Streamlink) SetOption(key, val string) {
	s.optionsMu.Lock()
	defer s.optionsMu.Unlock()
	s.options[key] = val
}

// Resolve finds the matching plugin for a given URL.
func (s *Streamlink) Resolve(rawURL string) (plugin.Plugin, *url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid URL: %w", err)
	}

	registryMu.RLock()
	registered := make([]plugin.Plugin, len(plugins))
	copy(registered, plugins)
	registryMu.RUnlock()

	for _, p := range registered {
		if p.CanHandle(u) {
			return p, u, nil
		}
	}

	return nil, nil, fmt.Errorf("no plugin found for %s", rawURL)
}

// Streams extracts available stream variants from the resolved plugin.
func (s *Streamlink) Streams(ctx context.Context, rawURL string) (map[string]stream.Stream, error) {
	p, u, err := s.Resolve(rawURL)
	if err != nil {
		return nil, err
	}
	return p.Streams(ctx, s, u)
}

// Best extracts the highest-quality stream variant for a URL.
func (s *Streamlink) Best(ctx context.Context, rawURL string) (stream.Stream, error) {
	streams, err := s.Streams(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	if len(streams) == 0 {
		return nil, errors.New("no streams found")
	}

	priorities := []string{"best", "source", "1080p60", "1080p", "720p60", "720p", "480p", "360p", "worst"}
	for _, q := range priorities {
		if st, ok := streams[q]; ok {
			return st, nil
		}
	}

	for _, st := range streams {
		return st, nil
	}
	return nil, errors.New("could not resolve quality")
}

var defaultSession = New()

// Streams wraps defaultSession.Streams.
func Streams(ctx context.Context, rawURL string) (map[string]stream.Stream, error) {
	return defaultSession.Streams(ctx, rawURL)
}

// Best wraps defaultSession.Best.
func Best(ctx context.Context, rawURL string) (stream.Stream, error) {
	return defaultSession.Best(ctx, rawURL)
}
