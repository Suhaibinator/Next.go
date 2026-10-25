//go:build deployment

package deployment

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

const sessionSigningKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type sessionExecutable struct {
	send   func(actionViewEvent) generatedActionReply
	close  func()
	origin string
	secure bool
}

type sessionFactory func(generatedActionScope, []string) *sessionExecutable

func sessionPage(scope generatedActionScope) string {
	path := scope.prefix() + "/cookie-session"
	if scope.slash {
		path += "/"
	}
	return path
}

func sessionMount(scope generatedActionScope) string {
	if scope.prefix() == "" || scope.slash {
		return scope.prefix() + "/"
	}
	return scope.prefix()
}

func sessionCookieName(host, mount string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(host) + "\x00" + mount))
	return fmt.Sprintf("nextgo_demo_session_%x", digest[:6])
}

func sessionNoSecrets(t *testing.T, reply generatedActionReply) {
	t.Helper()
	for _, secret := range []string{sessionSigningKey, adminToken, readerToken} {
		if bytes.Contains(reply.data, []byte(secret)) {
			t.Fatal("native session secret serialized into response data")
		}
	}
}

func sessionSSR(t *testing.T, reply generatedActionReply, role string, enabled bool) {
	t.Helper()
	sessionNoSecrets(t, reply)
	if reply.status != 200 || reply.headers.Get("Cache-Control") != "private, no-store" || reply.headers.Get("X-Robots-Tag") != "noindex" || !bytes.Contains(reply.data, []byte(`<title>Cookie session: `+role+` — Next.go</title>`)) || !bytes.Contains(reply.data, []byte(`data-session-role="">`+role+`</output>`)) || !bytes.Contains(reply.data, []byte(`data-session-enabled="`+strconv.FormatBool(enabled)+`"`)) {
		t.Fatalf("session SSR identity/privacy mismatch: %d %v %s", reply.status, reply.headers, reply.data)
	}
	if bytes.Contains(reply.data, []byte("data-session-admin-report")) != (role == "administrator") {
		t.Fatal("protected report does not match freshly authorized identity", role, string(reply.data))
	}
	meta := false
	for _, tag := range regexp.MustCompile(`<meta\b[^>]*>`).FindAll(reply.data, -1) {
		meta = meta || bytes.Contains(tag, []byte(`name="cookie-session-role"`)) && bytes.Contains(tag, []byte(`content="`+role+`"`))
	}
	if !meta {
		t.Fatal("session SSR role metadata missing", string(reply.data))
	}
}

func sessionCookie(t *testing.T, reply generatedActionReply, scope generatedActionScope, origin string, secure, deletion bool) *http.Cookie {
	t.Helper()
	values := (&http.Response{Header: reply.headers}).Cookies()
	u, _ := url.Parse(origin)
	if len(values) != 1 {
		t.Fatal("expected exactly one native response cookie", reply.headers)
	}
	cookie := values[0]
	if cookie.Name != sessionCookieName(u.Host, sessionMount(scope)) || cookie.Path != sessionMount(scope) || cookie.Domain != "" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Secure != secure {
		t.Fatalf("session cookie scope/flags mismatch: %+v", cookie)
	}
	if deletion {
		if cookie.Value != "" || cookie.MaxAge != -1 || !cookie.Expires.Before(time.Now()) {
			t.Fatal("logout did not delete the browser cookie", cookie)
		}
	} else if cookie.Value == "" || cookie.MaxAge <= 0 || cookie.MaxAge > 900 || !cookie.Expires.After(time.Now()) || cookie.Expires.After(time.Now().Add(901*time.Second)) {
		t.Fatal("signed cookie does not have a bounded fifteen-minute lifetime", cookie)
	}
	if cookie.Value != "" && bytes.Contains(reply.data, []byte(cookie.Value)) {
		t.Fatal("HttpOnly cookie value serialized in public action/view data")
	}
	return cookie
}

func sessionCombined(t *testing.T, reply generatedActionReply, role, page, build, configuration string) {
	t.Helper()
	sessionNoSecrets(t, reply)
	if err := generatedActionMetadata(reply, true); err != nil {
		t.Fatal(err)
	}
	if reply.status != 200 || !strings.HasPrefix(reply.headers.Get("Content-Type"), actionViewMedia) || reply.headers.Get("X-Nextgo-Action-View") != "1" || reply.headers.Get("Cache-Control") != "private, no-store" || reply.headers.Get("X-Nextgo-Configuration") != configuration || reply.headers.Get("X-Robots-Tag") != "noindex" {
		t.Fatal("cookie mutation did not produce a private combined stream", reply.status, reply.headers, string(reply.data))
	}
	var frames []actionViewDeploymentFrame
	for _, line := range bytes.Split(reply.data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var frame actionViewDeploymentFrame
		if err := json.Unmarshal(line, &frame); err != nil || frame.Version != 1 {
			t.Fatal("invalid cookie action frame", err, string(line))
		}
		frames = append(frames, frame)
	}
	if len(frames) != 3 || frames[0].Type != "result" || frames[1].Type != "shell" || frames[2].Type != "done" {
		t.Fatal("cookie confirmation/fresh shell/completion order", string(reply.data))
	}
	var result struct {
		Data     map[string]string `json:"data"`
		Redirect string            `json:"redirect"`
	}
	if err := json.Unmarshal(frames[0].Result, &result); err != nil || len(result.Data) != 1 || result.Data["role"] != role || result.Redirect != page {
		t.Fatal("cookie action result must contain only public role and scoped redirect", result, err)
	}
	var view struct {
		Version, Status                        int
		Build, Configuration, URL, HTML, Title string
		Meta                                   map[string]string
	}
	if err := json.Unmarshal(frames[1].Page, &view); err != nil || view.Version != navigation.ProtocolVersion || view.Status != 200 || view.Build != build || view.Configuration != configuration || view.URL != page || view.Title != "Cookie session: "+role+" — Next.go" || view.Meta["cookie-session-role"] != role || !strings.Contains(view.HTML, `data-session-role="">`+role+`</output>`) || !strings.Contains(view.HTML, `data-session-panel-role="">`+role+`</output>`) {
		t.Fatal("same POST did not authorize/render the newly staged cookie", view, err)
	}
	if strings.Contains(view.HTML, "data-session-admin-report") != (role == "administrator") {
		t.Fatal("same-POST protected report authorization mismatch", role, view.HTML)
	}
}

func exerciseCookieSession(t *testing.T, factory sessionFactory, scope generatedActionScope) {
	t.Helper()
	worker := factory(scope, nil)
	defer worker.close()
	page := sessionPage(scope)
	get := func(path, cookie string, extra http.Header) generatedActionReply {
		h := extra.Clone()
		if h == nil {
			h = make(http.Header)
		}
		if cookie != "" {
			h.Set("Cookie", cookie)
		}
		return worker.send(actionViewEvent{method: "GET", path: path, headers: h})
	}
	initial := get(page, "", nil)
	sessionSSR(t, initial, "anonymous", true)
	b := regexp.MustCompile(`data-nextgo-build="([^"]+)"`).FindSubmatch(initial.data)
	c := configurationRE.FindSubmatch(initial.data)
	if len(b) != 2 || len(c) != 2 || initial.headers.Get("X-Nextgo-Configuration") != string(c[1]) {
		t.Fatal("session document lacks current build/configuration")
	}
	post := func(action, payload, cookie string, combined, form bool) generatedActionReply {
		h := http.Header{"Origin": {worker.origin}, "Content-Type": {"application/json"}, "Accept": {"application/json"}, "X-Nextgo-Action-Protocol": {"1"}, "X-Nextgo-Action-Revision": {"v1"}}
		if cookie != "" {
			h.Set("Cookie", cookie)
		}
		if combined {
			h.Set("Accept", actionViewMedia)
			h.Set("X-Nextgo-Action-View", "1")
			h.Set("X-Nextgo-Action-URL", page)
			h.Set("X-Nextgo-Action-Build", string(b[1]))
			h.Set("X-Nextgo-Configuration", string(c[1]))
		}
		if form {
			h.Set("Accept", "text/html")
			h.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		return worker.send(actionViewEvent{method: "POST", path: scope.prefix() + "/_nextgo/actions/session/" + action + "/v1", payload: payload, headers: h})
	}
	for _, test := range []struct {
		payload, code string
		status        int
	}{{`{`, "invalid_body", 400}, {`{"credential":"short"}`, "validation_error", 422}, {`{"credential":"` + strings.Repeat("wrong", 12) + `"}`, "unauthorized", 401}} {
		reply := post("login", test.payload, "", true, false)
		if err := generatedActionFailure(reply, test.status, test.code, true); err != nil {
			t.Fatal(err)
		}
		sessionNoSecrets(t, reply)
		if len(reply.headers.Values("Set-Cookie")) != 0 || !strings.HasPrefix(reply.headers.Get("Content-Type"), "application/json") {
			t.Fatal("failed login staged a cookie or confirmed stream", reply.headers)
		}
	}
	login := post("login", `{"credential":"`+adminToken+`"}`, "", true, false)
	sessionCombined(t, login, "administrator", page, string(b[1]), string(c[1]))
	admin := sessionCookie(t, login, scope, worker.origin, worker.secure, false)
	copied := admin.Name + "=" + admin.Value
	sessionSSR(t, get(page, copied, nil), "administrator", true)
	sessionSSR(t, get(page+"?role=administrator&credential=untrusted", "", http.Header{"Authorization": {"Bearer " + adminToken}, "X-Role": {"administrator"}}), "anonymous", true)
	tampered := admin.Name + "=X" + admin.Value[1:]
	sessionSSR(t, get(page, tampered, nil), "anonymous", true)
	u, _ := url.Parse(worker.origin)
	otherHost := "127.0.0.1:44002"
	if worker.secure {
		otherHost = "other-cookie-session.test"
	}
	// Renaming to the new scope's expected cookie name prevents a mere name
	// mismatch from passing these MAC-binding checks.
	wrongHostCookie := sessionCookieName(otherHost, sessionMount(scope)) + "=" + admin.Value
	sessionSSR(t, get(page, wrongHostCookie, http.Header{"Host": {otherHost}}), "anonymous", true)
	otherScope := generatedActionScope{base: "/other", locale: "fr", slash: true}
	other := factory(otherScope, nil)
	wrongMountCookie := sessionCookieName(u.Host, sessionMount(otherScope)) + "=" + admin.Value
	sessionSSR(t, other.send(actionViewEvent{method: "GET", path: sessionPage(otherScope), headers: http.Header{"Cookie": {wrongMountCookie}}}), "anonymous", true)
	other.close()
	cold := factory(scope, nil)
	sessionSSR(t, cold.send(actionViewEvent{method: "GET", path: page, headers: http.Header{"Cookie": {copied}}}), "administrator", true)
	cold.close()
	logout := post("logout", `{}`, copied, true, false)
	sessionCombined(t, logout, "anonymous", page, string(b[1]), string(c[1]))
	cleared := sessionCookie(t, logout, scope, worker.origin, worker.secure, true)
	if cleared.Name != admin.Name {
		t.Fatal("logout cleared a different cookie scope")
	}
	sessionSSR(t, get(page, "", nil), "anonymous", true)
	// Explicitly qualify the sample's documented limitation: clearing the
	// browser does not revoke a copied, still-valid stateless token.
	sessionSSR(t, get(page, copied, nil), "administrator", true)
	form := post("login", url.Values{"credential": {readerToken}}.Encode(), "", false, true)
	if form.status != 303 || form.headers.Get("Location") != page {
		t.Fatal("native cookie login lost scoped redirect", form.status, form.headers)
	}
	reader := sessionCookie(t, form, scope, worker.origin, worker.secure, false)
	readerPair := reader.Name + "=" + reader.Value
	sessionSSR(t, get(page, readerPair, nil), "reader", true)
	formLogout := post("logout", "", readerPair, false, true)
	if formLogout.status != 303 || formLogout.headers.Get("Location") != page {
		t.Fatal("native cookie logout lost scoped redirect", formLogout.status, formLogout.headers)
	}
	sessionCookie(t, formLogout, scope, worker.origin, worker.secure, true)
	for _, reply := range []generatedActionReply{form, formLogout} {
		sessionNoSecrets(t, reply)
	}
}

func newSessionHTTP(t *testing.T, scope generatedActionScope, extra []string) *sessionExecutable {
	t.Helper()
	addr := address(t)
	env := append(scope.environment(), "NEXTGO_DEMO_COOKIE_KEY="+sessionSigningKey, "NEXTGO_DEMO_COOKIE_ALLOW_INSECURE=true")
	p := start(t, append(env, extra...), "-addr", addr)
	client := &http.Client{Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	origin := "http://127.0.0.1:44001"
	for deadline := time.Now().Add(7 * time.Second); ; {
		request, _ := http.NewRequest("GET", "http://"+addr+sessionPage(scope), nil)
		request.Host = "127.0.0.1:44001"
		response, err := client.Do(request)
		if err == nil {
			_ = body(t, response)
			if response.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("cookie-session readiness timeout", p.logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	var closed bool
	closeWorker := func() {
		if !closed {
			closed = true
			client.CloseIdleConnections()
			p.stop(t)
		}
	}
	t.Cleanup(func() { client.CloseIdleConnections() })
	return &sessionExecutable{origin: origin, close: closeWorker, send: func(event actionViewEvent) generatedActionReply {
		request, err := http.NewRequest(event.method, "http://"+addr+event.path, strings.NewReader(event.payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header = event.headers.Clone()
		request.Host = "127.0.0.1:44001"
		if host := request.Header.Get("Host"); host != "" {
			request.Host = host
			request.Header.Del("Host")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return generatedActionReply{response.StatusCode, response.Header.Clone(), body(t, response)}
	}}
}

func newSessionLambda(t *testing.T, mode string, scope generatedActionScope, extra []string) *sessionExecutable {
	t.Helper()
	events := make(chan actionViewEvent)
	replies := make(chan generatedActionReply, 1)
	var issued atomic.Int32
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/next") {
			var event actionViewEvent
			select {
			case event = <-events:
			case <-r.Context().Done():
				return
			}
			u, _ := url.Parse(event.path)
			headers := map[string]string{"host": "cookie-session.test"}
			var cookies []string
			for name, values := range event.headers {
				if strings.EqualFold(name, "Cookie") {
					cookies = append(cookies, values...)
				} else {
					headers[strings.ToLower(name)] = strings.Join(values, ",")
				}
			}
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(int(issued.Add(1))))
			w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(8*time.Second).UnixMilli(), 10))
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": u.Path, "rawQueryString": u.RawQuery, "headers": headers, "cookies": cookies, "body": event.payload, "requestContext": map[string]any{"domainName": headers["host"], "http": map[string]string{"method": event.method, "path": u.Path}}})
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			t.Error(err)
		}
		var envelope struct {
			httpEnvelope
			Cookies []string `json:"cookies"`
		}
		var payload []byte
		if mode == "buffered" {
			err = json.Unmarshal(data, &envelope)
			payload = []byte(envelope.Body)
			if envelope.Base64 {
				payload, err = base64.StdEncoding.DecodeString(envelope.Body)
			}
		} else if split := bytes.Index(data, make([]byte, 8)); split >= 0 {
			err = json.Unmarshal(data[:split], &envelope)
			payload = data[split+8:]
		} else {
			err = fmt.Errorf("missing cookie Lambda response metadata: %s", data)
		}
		if err != nil {
			t.Error(err)
		}
		headers := make(http.Header)
		for name, value := range envelope.Headers {
			if strings.EqualFold(name, "Set-Cookie") {
				t.Error("Lambda must use the cookies array, not fold Set-Cookie metadata")
			}
			headers.Set(name, value)
		}
		for _, value := range envelope.Cookies {
			headers.Add("Set-Cookie", value)
		}
		replies <- generatedActionReply{envelope.StatusCode, headers, payload}
		w.WriteHeader(202)
	}))
	t.Cleanup(runtime.Close)
	env := append(scope.environment(), "NEXTGO_DEMO_COOKIE_KEY="+sessionSigningKey, "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+mode)
	p := start(t, append(env, extra...))
	var closed bool
	closeWorker := func() {
		if !closed {
			closed = true
			p.stop(t)
			runtime.Close()
		}
	}
	t.Cleanup(func() { closeSeedChild(p); runtime.Close() })
	return &sessionExecutable{origin: "https://cookie-session.test", secure: true, close: closeWorker, send: func(event actionViewEvent) generatedActionReply {
		select {
		case events <- event:
		case <-p.done:
			t.Fatal("cookie worker exited", p.err, p.logs.String())
		case <-time.After(10 * time.Second):
			t.Fatal("cookie worker did not request next event", p.logs.String())
		}
		select {
		case reply := <-replies:
			return reply
		case <-p.done:
			t.Fatal("cookie worker exited before response", p.err, p.logs.String())
		case <-time.After(10 * time.Second):
			t.Fatal("cookie Lambda response timeout", p.logs.String())
		}
		return generatedActionReply{}
	}}
}

func exerciseSessionDisabled(t *testing.T, worker *sessionExecutable) {
	t.Helper()
	defer worker.close()
	page := sessionPage(generatedActionScope{})
	sessionSSR(t, worker.send(actionViewEvent{method: "GET", path: page}), "anonymous", false)
	reply := worker.send(actionViewEvent{method: "POST", path: "/_nextgo/actions/session/login/v1", payload: `{"credential":"` + adminToken + `"}`, headers: http.Header{"Origin": {worker.origin}, "Content-Type": {"application/json"}, "Accept": {"application/json"}, "X-Nextgo-Action-Protocol": {"1"}, "X-Nextgo-Action-Revision": {"v1"}}})
	if err := generatedActionFailure(reply, 403, "forbidden", true); err != nil {
		t.Fatal(err)
	}
	if len(reply.headers.Values("Set-Cookie")) != 0 {
		t.Fatal("disabled signing/transport minted a cookie", reply.headers)
	}
	sessionNoSecrets(t, reply)
}

func TestCookieSessionHTTPExecutable(t *testing.T) {
	for _, scope := range []generatedActionScope{{}, {base: "/scoped", locale: "fr", slash: true}} {
		t.Run(scope.prefix(), func(t *testing.T) {
			exerciseCookieSession(t, func(s generatedActionScope, extra []string) *sessionExecutable { return newSessionHTTP(t, s, extra) }, scope)
		})
	}
	t.Run("missing-key", func(t *testing.T) {
		exerciseSessionDisabled(t, newSessionHTTP(t, generatedActionScope{}, []string{"NEXTGO_DEMO_COOKIE_KEY="}))
	})
	t.Run("HTTP-default-disabled", func(t *testing.T) {
		exerciseSessionDisabled(t, newSessionHTTP(t, generatedActionScope{}, []string{"NEXTGO_DEMO_COOKIE_ALLOW_INSECURE=false"}))
	})
}

func TestCookieSessionLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		for _, scope := range []generatedActionScope{{}, {base: "/scoped", locale: "fr", slash: true}} {
			t.Run(mode+scope.prefix(), func(t *testing.T) {
				exerciseCookieSession(t, func(s generatedActionScope, extra []string) *sessionExecutable {
					return newSessionLambda(t, mode, s, extra)
				}, scope)
			})
		}
		t.Run(mode+"/missing-key", func(t *testing.T) {
			exerciseSessionDisabled(t, newSessionLambda(t, mode, generatedActionScope{}, []string{"NEXTGO_DEMO_COOKIE_KEY="}))
		})
	}
}
