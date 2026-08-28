package generic

import (
	"context"
	"net/url"
	"strings"

	"github.com/LaPingvino/streamglink"
	"github.com/LaPingvino/streamglink/plugin"
	"github.com/LaPingvino/streamglink/stream"
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
			PluginName: "generic",
			MatchList: []*plugin.Matcher{
				plugin.NewMatcher(`\.(?:m3u8|mpd|ts)(?:\?.*)?$`, plugin.PriorityLow),
			},
		},
	}
}

func (p *Plugin) Streams(ctx context.Context, session plugin.SessionInterface, u *url.URL) (map[string]stream.Stream, error) {
	raw := u.String()
	headers := map[string]string{
		"User-Agent": session.UserAgent(),
	}

	if strings.Contains(strings.ToLower(u.Path), ".m3u8") {
		st := stream.NewHLSStream("live", raw, raw, headers, session.HTTPClient())
		return map[string]stream.Stream{
			"live": st,
			"best": st,
		}, nil
	}

	st := stream.NewHTTPStream("live", raw, headers, session.HTTPClient())
	return map[string]stream.Stream{
		"live": st,
		"best": st,
	}, nil
}
