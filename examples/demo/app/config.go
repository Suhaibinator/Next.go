//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/examples/demo/access"
	"github.com/Suhaibinator/Next.go/examples/demo/media"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
	"github.com/Suhaibinator/Next.go/examples/demo/preview"
	"github.com/Suhaibinator/Next.go/examples/demo/state"
	"github.com/Suhaibinator/Next.go/mutation"
	"github.com/Suhaibinator/Next.go/telemetry"
	"github.com/Suhaibinator/Next.go/telemetry/otlp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Configure is called by generated entrypoints before starting the application.
func Configure(config *nextgo.Config) {
	configureStreaming(config)
	configurePartialControls(config)
	configureScripts(config)
	configureHeaderRules(config)
	message := os.Getenv("NEXTGO_DEMO_PUBLIC_MESSAGE")
	if message == "" {
		message = "Configured at server startup"
	}
	config.Public = map[string]string{"demo.message": message}
	if os.Getenv("NEXTGO_DEMO_RULES") == "true" {
		config.Redirects = append(config.Redirects, nextgo.RedirectRule{Source: "/rules/jump/:slug", Destination: "/posts/:slug", Has: []nextgo.RuleCondition{{Type: nextgo.QueryCondition, Key: "go", Value: "yes"}}})
		config.Rewrites = append(config.Rewrites,
			nextgo.RewriteRule{Source: "/rules/choose", Destination: "/posts/:selected", Has: []nextgo.RuleCondition{{Type: nextgo.QueryCondition, Key: "post", Value: "(?<selected>html-first|go-islands)"}}},
			nextgo.RewriteRule{Source: "/rules/header", Destination: "/posts/go-islands", Has: []nextgo.RuleCondition{{Type: nextgo.HeaderCondition, Key: "X-Demo-Route", Value: "go"}}},
			nextgo.RewriteRule{Source: "/rules/header", Destination: "/posts/html-first"},
			nextgo.RewriteRule{Source: "/rules/cookie", Destination: "/posts/go-islands", Has: []nextgo.RuleCondition{{Type: nextgo.CookieCondition, Key: "nextgo_variant", Value: "go"}}},
			nextgo.RewriteRule{Source: "/rules/cookie", Destination: "/posts/html-first"},
			nextgo.RewriteRule{Source: "/rules/host", Destination: "/about", Has: []nextgo.RuleCondition{{Type: nextgo.HostCondition, Value: `localhost|127\.0\.0\.1`}}})
	}
	if origin := os.Getenv("NEXTGO_DEMO_PROXY_ORIGIN"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
			panic("demo proxy origin must be an HTTP(S) authority without path/credentials/query")
		}
		config.Rewrites = append(config.Rewrites, nextgo.RewriteRule{Source: "/rules/remote/*tail", Destination: strings.TrimRight(origin, "/") + "/*tail"})
		config.Redirects = append(config.Redirects, nextgo.RedirectRule{Source: "/rules/external", Destination: strings.TrimRight(origin, "/") + "/document", Has: []nextgo.RuleCondition{{Type: nextgo.QueryCondition, Key: "go", Value: "yes"}}})
		if value := os.Getenv("NEXTGO_DEMO_PROXY_TIMEOUT"); value != "" {
			timeout, err := time.ParseDuration(value)
			if err != nil {
				panic("invalid demo proxy timeout")
			}
			config.Proxy.Timeout = timeout
		}
	}

	if os.Getenv("NEXTGO_DEMO_TRACING") == "true" || os.Getenv("NEXTGO_DEMO_OTLP_ENDPOINT") != "" {
		options := telemetry.Options{Recorder: telemetry.JSONRecorder(os.Stderr), TrustIncoming: os.Getenv("NEXTGO_DEMO_TRACE_TRUST_INCOMING") == "true"}
		if endpoint := os.Getenv("NEXTGO_DEMO_OTLP_ENDPOINT"); endpoint != "" {
			exporter, err := otlp.New(otlp.Options{Endpoint: endpoint, ServiceName: "nextgo-demo"})
			if err != nil {
				panic(err)
			}
			options.Recorder, options.Flush, options.Shutdown = exporter.Record, exporter.Flush, exporter.Shutdown
		}
		tracer, err := telemetry.New(options)
		if err != nil {
			panic(err)
		}
		config.Tracing = tracer
	}
	if os.Getenv("NEXTGO_DEMO_LOCALES") == "true" {
		config.Locales = &nextgo.LocaleConfig{Codes: []string{"en", "fr", "ar"}, Default: "en", PrefixDefault: true, Detect: true, Directions: map[string]string{"ar": "rtl"}}
		config.Locales.Detect = os.Getenv("NEXTGO_DEMO_LOCALE_DETECT") != "false"
		config.Locales.PrefixDefault = os.Getenv("NEXTGO_DEMO_LOCALE_PREFIX_DEFAULT") != "false"
		if domains := os.Getenv("NEXTGO_DEMO_LOCALE_DOMAINS"); domains != "" {
			config.Locales.PrefixDefault = false
			for _, entry := range strings.Split(domains, ",") {
				host, code, _ := strings.Cut(entry, "=")
				config.Locales.Domains = append(config.Locales.Domains, nextgo.LocaleDomain{Host: host, Default: code, HTTP: true})
			}
		}
	}
	config.TrailingSlash = os.Getenv("NEXTGO_DEMO_TRAILING_SLASH") == "true"
	config.BasePath = os.Getenv("NEXTGO_DEMO_BASE_PATH")
	config.AssetPrefix = os.Getenv("NEXTGO_DEMO_ASSET_PREFIX")
	config.PreserveLayouts = true
	config.ViewTransitions = os.Getenv("NEXTGO_DEMO_VIEW_TRANSITIONS") == "true"
	config.Cache = state.Cache
	if config.CacheProfiles == nil {
		config.CacheProfiles = map[string]cache.Profile{}
	}
	if config.CacheProfileSpecs == nil {
		config.CacheProfileSpecs = map[string]cache.ProfileSpec{}
	}
	// An empty partial definition inherits every age from the effective default.
	config.CacheProfileSpecs["demo-evergreen"] = cache.ProfileSpec{}
	config.CacheProfiles["demo-profile"] = cache.Profile{Stale: 2 * time.Second, Revalidate: time.Second, Expire: 8 * time.Second}
	configureSharedPages(config)
	configureGeneratedActions(config)
	config.Images = media.Handler()
	if config.Handlers == nil {
		config.Handlers = media.CrawlersWithPaths(nextgo.PathConfig{BasePath: config.BasePath, TrailingSlash: config.TrailingSlash})
	} else {
		for path, handler := range media.CrawlersWithPaths(nextgo.PathConfig{BasePath: config.BasePath, TrailingSlash: config.TrailingSlash}) {
			config.Handlers[path] = handler
		}
	}
	for i := range config.Routes {
		// Link feedback demonstrates a real complete public navigation-cache hit.
		if config.Routes[i].Pattern == "/link-status/one" {
			config.Routes[i].Cache = &nextgo.CachePolicy{TTL: time.Minute}
		}
		if config.Routes[i].Pattern == "/preview" {
			config.Routes[i].Cache = &nextgo.CachePolicy{TTL: 10 * time.Second}
		}
		if config.Routes[i].Pattern == "/posts" {
			// The example post collection contains no personalized data.
			config.Routes[i].Cache = &nextgo.CachePolicy{TTL: 10 * time.Second, Tags: []string{"posts"}}
		}
		if config.Routes[i].Pattern == "/cache" {
			config.Routes[i].Cache = &nextgo.CachePolicy{Lifetime: &cache.Lifetime{Revalidate: time.Second, Expire: 5 * time.Second}, Tags: []string{"cache-demo"}}
		}
		if config.Routes[i].Pattern == "/evergreen" {
			config.Routes[i].Cache = &nextgo.CachePolicy{Profile: "demo-evergreen", Tags: []string{"evergreen-demo"}}
		}
		if config.Routes[i].Pattern == "/profiles" {
			config.Routes[i].Cache = &nextgo.CachePolicy{Profile: "demo-profile", Tags: []string{"profile-demo"}}
		}
	}
	preview.Configure(config)
	router := actions.New(actions.Options{UnauthorizedChallenge: `Bearer realm="nextgo-demo"`, OnRevalidate: func(tags, paths []string) {
		for _, tag := range tags {
			state.Cache.ExpireTag(tag)
		}
		for _, path := range paths {
			state.Cache.ExpirePathPrefix(path)
		}
	}})
	configureProfileForm(router)
	configureChoices(router)
	configureUploads(router)
	configureSharedCache(router)
	configureSharedPageActions(router)
	configureSharedPrerenderActions(router)
	// Independent public demo data keeps optimistic tests separate from the
	// ordinary counter/cache examples. Production handlers need their own auth.
	mutations.Increment.Bind(router, func(c *actions.Context, input mutations.IncrementInput) (mutation.Result[mutations.IncrementOutput], error) {
		if input.Amount < 1 || input.Amount > 5 {
			return mutation.Result[mutations.IncrementOutput]{}, &actions.ValidationError{Message: "Choose an amount from 1 to 5.", Fields: map[string]string{"amount": "Must be between 1 and 5."}}
		}
		if err := c.Err(); err != nil {
			return mutation.Result[mutations.IncrementOutput]{}, err
		}
		applied := min(input.Amount, 2)
		return mutation.Result[mutations.IncrementOutput]{Data: mutations.IncrementOutput{Applied: applied, Count: state.OptimisticIncrement(applied)}, Redirect: "/optimistic", RevalidatePaths: []string{"/optimistic"}}, nil
	}, mutation.BindOptions[mutations.IncrementInput]{Form: func(c *actions.Context, form url.Values) (mutations.IncrementInput, error) {
		amount, err := strconv.ParseInt(form.Get("amount"), 10, 64)
		if err != nil {
			return mutations.IncrementInput{}, &actions.ValidationError{Message: "Choose an amount from 1 to 5.", Fields: map[string]string{"amount": "Enter a whole number from 1 to 5."}}
		}
		return mutations.IncrementInput{Amount: amount}, nil
	}})

	// This intentionally public demo action only changes a process-local counter.
	// Real mutations must authenticate and authorize inside their handler.
	router.Handle("increment", func(c *actions.Context) (actions.Result, error) {
		amount := int64(1)
		if strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
			var input struct {
				Amount *int64 `json:"amount"`
			}
			if err := c.DecodeJSON(&input); err != nil {
				return actions.Result{}, err
			}
			if input.Amount != nil {
				amount = *input.Amount
			}
		} else {
			form, err := c.ParseForm()
			if err != nil {
				return actions.Result{}, err
			}
			if raw := form.Get("amount"); raw != "" {
				amount, err = strconv.ParseInt(raw, 10, 64)
				if err != nil {
					return actions.Result{}, &actions.ValidationError{Message: "Enter a whole-number amount.", Fields: map[string]string{"amount": "Enter a number from 1 to 10."}}
				}
			}
		}
		if amount < 1 || amount > 10 {
			return actions.Result{}, &actions.ValidationError{Message: "Choose an amount from 1 to 10.", Fields: map[string]string{"amount": "Must be between 1 and 10."}}
		}
		if err := c.Err(); err != nil {
			return actions.Result{}, err
		}
		return actions.Result{Data: map[string]int64{"count": state.Increment(amount)}, Redirect: "/features", RevalidateTags: []string{"posts", "counter", "cache-demo", "profile-demo", "evergreen-demo"}, RevalidatePaths: []string{"/features", "/posts"}}, nil
	})
	// This public illustrative source update intentionally leaves snapshots
	// valid, demonstrating bounded eventual refresh rather than publication.
	router.Handle("cache-source-increment", func(*actions.Context) (actions.Result, error) {
		return actions.Result{Data: map[string]int64{"count": state.Increment(1)}, Redirect: "/cache"}, nil
	})
	// Commit first, then mark only the profile demonstration stale. Unlike
	// increment, this allows a last-good snapshot during the bounded refresh.
	publishStale := func(tag, profileName, destination string) actions.Handler {
		return func(c *actions.Context) (actions.Result, error) {
			profile, err := nextgo.LookupCacheProfile(c.Context, profileName)
			if err != nil {
				return actions.Result{}, err
			}
			if err := c.Err(); err != nil {
				return actions.Result{}, err
			}
			count := state.Increment(1)
			affected, err := state.Cache.MarkStaleTag(tag, profile.Expire)
			return actions.Result{Data: map[string]int64{"count": count, "affected": int64(affected)}, Redirect: destination}, err
		}
	}
	router.Handle("cache-publish-stale", publishStale("profile-demo", "demo-profile", "/profiles"))
	router.Handle("evergreen-publish", publishStale("evergreen-demo", "demo-evergreen", "/evergreen"))
	router.Handle("review-report", func(c *actions.Context) (actions.Result, error) {
		// Repeat the data-level authorization check inside the mutation. A
		// hidden form or successful page request never grants action access.
		if _, err := access.ReadReport(c.Context, c.Request); err != nil {
			return actions.Result{}, err
		}
		if strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
			if err := c.DecodeJSON(&struct{}{}); err != nil {
				return actions.Result{}, err
			}
		} else {
			if _, err := c.ParseForm(); err != nil {
				return actions.Result{}, err
			}
		}
		if err := c.Err(); err != nil {
			return actions.Result{}, err
		}
		return actions.Result{Data: map[string]int64{"reviews": state.ReviewReport()}, Redirect: "/restricted", RevalidatePaths: []string{"/restricted"}}, nil
	})
	config.Actions = router
}
