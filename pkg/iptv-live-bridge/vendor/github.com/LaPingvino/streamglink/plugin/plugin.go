package plugin

import (
	"context"
	"net/http"
	"net/url"
	"regexp"

	"github.com/LaPingvino/streamglink/stream"
)

// Priority defines plugin precedence when multiple plugins match a URL.
type Priority int

const (
	PriorityLow    Priority = 10
	PriorityNormal Priority = 50
	PriorityHigh   Priority = 90
)

// SessionInterface decouples plugins from the concrete Streamlink session.
type SessionInterface interface {
	HTTPClient() *http.Client
	UserAgent() string
	GetOption(key string) (string, bool)
	SetOption(key, val string)
}

// Matcher associates a regex pattern with a priority.
type Matcher struct {
	Pattern  *regexp.Regexp
	Priority Priority
}

func NewMatcher(regexStr string, priority Priority) *Matcher {
	return &Matcher{
		Pattern:  regexp.MustCompile(regexStr),
		Priority: priority,
	}
}

// Plugin is the base interface that all stream extractors must implement.
type Plugin interface {
	Name() string
	Matchers() []*Matcher
	CanHandle(u *url.URL) bool
	Streams(ctx context.Context, session SessionInterface, u *url.URL) (map[string]stream.Stream, error)
}

// BasePlugin provides default helper implementations for plugins.
type BasePlugin struct {
	PluginName string
	MatchList  []*Matcher
}

func (b *BasePlugin) Name() string {
	return b.PluginName
}

func (b *BasePlugin) Matchers() []*Matcher {
	return b.MatchList
}

func (b *BasePlugin) CanHandle(u *url.URL) bool {
	raw := u.String()
	for _, m := range b.MatchList {
		if m.Pattern.MatchString(raw) {
			return true
		}
	}
	return false
}
