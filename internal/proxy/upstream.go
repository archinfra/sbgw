package proxy

import (
	"math/rand"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/archinfra/sbgw/internal/config"
)

type upstreamEndpoint struct {
	cfg      config.UpstreamEndpointConfig
	url      *url.URL
	models   map[string]struct{}
	inflight int64
}

type upstreamPool struct {
	strategy string
	items    []*upstreamEndpoint
	rr       atomic.Uint64
}

func newUpstreamPool(cfg config.UpstreamConfig) (*upstreamPool, error) {
	items := make([]*upstreamEndpoint, 0, len(cfg.Endpoints))
	for _, epCfg := range cfg.Endpoints {
		u, err := url.Parse(strings.TrimRight(epCfg.BaseURL, "/"))
		if err != nil {
			return nil, err
		}
		models := map[string]struct{}{}
		for _, m := range epCfg.Models {
			m = strings.TrimSpace(m)
			if m != "" {
				models[m] = struct{}{}
			}
		}
		items = append(items, &upstreamEndpoint{cfg: epCfg, url: u, models: models})
	}
	return &upstreamPool{strategy: cfg.Strategy, items: items}, nil
}

func (p *upstreamPool) selectEndpoint(model string, endpointNames []string) *upstreamEndpoint {
	allowed := allowedEndpointNames(endpointNames)
	candidates := make([]*upstreamEndpoint, 0, len(p.items))
	for _, ep := range p.items {
		if !endpointAllowed(ep.cfg.Name, allowed) {
			continue
		}
		if ep.supports(model) {
			candidates = append(candidates, ep)
		}
	}
	if len(candidates) == 0 {
		for _, ep := range p.items {
			if !endpointAllowed(ep.cfg.Name, allowed) {
				continue
			}
			if len(ep.models) == 0 {
				candidates = append(candidates, ep)
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	switch p.strategy {
	case "round_robin":
		return candidates[int(p.rr.Add(1)-1)%len(candidates)]
	case "random":
		return candidates[rand.New(rand.NewSource(time.Now().UnixNano())).Intn(len(candidates))]
	case "weighted_random":
		return weightedRandom(candidates)
	case "least_inflight":
		return leastInflight(candidates)
	case "weighted_round_robin":
		fallthrough
	default:
		return weightedRoundRobin(candidates, p.rr.Add(1)-1)
	}
}

func allowedEndpointNames(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func endpointAllowed(name string, allowed map[string]struct{}) bool {
	if len(allowed) == 0 {
		return true
	}
	_, ok := allowed[name]
	return ok
}

func (e *upstreamEndpoint) supports(model string) bool {
	if len(e.models) == 0 || model == "" {
		return true
	}
	_, ok := e.models[model]
	return ok
}

func (e *upstreamEndpoint) begin() int64 {
	return atomic.AddInt64(&e.inflight, 1)
}

func (e *upstreamEndpoint) end() int64 {
	return atomic.AddInt64(&e.inflight, -1)
}

func (e *upstreamEndpoint) currentInflight() int64 {
	return atomic.LoadInt64(&e.inflight)
}

func weightedRoundRobin(candidates []*upstreamEndpoint, n uint64) *upstreamEndpoint {
	total := 0
	for _, ep := range candidates {
		total += weight(ep)
	}
	if total <= 0 {
		return candidates[int(n)%len(candidates)]
	}
	idx := int(n % uint64(total))
	for _, ep := range candidates {
		w := weight(ep)
		if idx < w {
			return ep
		}
		idx -= w
	}
	return candidates[0]
}

func weightedRandom(candidates []*upstreamEndpoint) *upstreamEndpoint {
	total := 0
	for _, ep := range candidates {
		total += weight(ep)
	}
	if total <= 0 {
		return candidates[rand.New(rand.NewSource(time.Now().UnixNano())).Intn(len(candidates))]
	}
	n := rand.New(rand.NewSource(time.Now().UnixNano())).Intn(total)
	for _, ep := range candidates {
		w := weight(ep)
		if n < w {
			return ep
		}
		n -= w
	}
	return candidates[0]
}

func leastInflight(candidates []*upstreamEndpoint) *upstreamEndpoint {
	sort.SliceStable(candidates, func(i, j int) bool {
		a := candidates[i]
		b := candidates[j]
		if a.currentInflight() == b.currentInflight() {
			return weight(a) > weight(b)
		}
		return a.currentInflight() < b.currentInflight()
	})
	return candidates[0]
}

func weight(ep *upstreamEndpoint) int {
	if ep.cfg.Weight <= 0 {
		return 1
	}
	return ep.cfg.Weight
}

func (p *upstreamPool) modelIDs(modelMap map[string]string, routes []config.RouteConfig) []string {
	seen := map[string]struct{}{}
	for logical := range modelMap {
		logical = strings.TrimSpace(logical)
		if logical != "" {
			seen[logical] = struct{}{}
		}
	}

	for _, route := range routes {
		model := strings.TrimSpace(route.Model)
		if model != "" {
			seen[model] = struct{}{}
		}
	}
	for _, ep := range p.items {
		for model := range ep.models {
			seen[model] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for model := range seen {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}
