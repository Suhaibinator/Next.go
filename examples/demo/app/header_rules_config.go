//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"os"
	"time"
)

func configureHeaderRules(config *nextgo.Config) {
	if os.Getenv("NEXTGO_DEMO_HEADER_RULES") != "true" && os.Getenv("NEXTGO_DEMO_RULES") != "true" {
		return
	}
	config.HeaderRules = append(config.HeaderRules,
		nextgo.HeaderRule{Source: "/rules/headers/:slug", Headers: []nextgo.HeaderValue{{Name: "X-Demo-Stage", Value: "base"}, {Name: "X-Demo-Slug", Value: "{{slug}}"}, {Name: "Link", Value: "</docs/base>; rel=help"}}},
		nextgo.HeaderRule{Source: "/rules/headers/:slug", Has: []nextgo.RuleCondition{{Type: nextgo.QueryCondition, Key: "variant", Value: `(?<variant>blue|green)`}}, Missing: []nextgo.RuleCondition{{Type: nextgo.CookieCondition, Key: "demo_header_skip"}}, Headers: []nextgo.HeaderValue{{Name: "X-Demo-Stage", Value: "selected"}, {Name: "X-Demo-Variant", Value: "{{variant}}"}, {Name: "Link", Value: "</docs/selected>; rel=help", Append: true}}},
		nextgo.HeaderRule{Source: "/rules/headers/:slug", Has: []nextgo.RuleCondition{{Type: nextgo.QueryCondition, Key: "raw", Value: `(?<raw>[\s\S]*)`}}, Headers: []nextgo.HeaderValue{{Name: "X-Demo-Raw", Value: "{{raw}}"}}},
		nextgo.HeaderRule{Source: "/api/header-policy", Has: []nextgo.RuleCondition{{Type: nextgo.HeaderCondition, Key: "X-Demo-Region", Value: `(?<region>[a-z-]+)`}}, Headers: []nextgo.HeaderValue{{Name: "X-Demo-Region", Value: "{{region}}"}}},
		nextgo.HeaderRule{Source: "/assets/*path", Headers: []nextgo.HeaderValue{{Name: "X-Demo-Asset", Value: "{{path}}"}}},
	)
	for i := range config.Routes {
		if config.Routes[i].Pattern == "/rules/headers/:slug" {
			config.Routes[i].Cache = &nextgo.CachePolicy{TTL: time.Minute}
		}
	}
}
