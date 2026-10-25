package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Inspect reads one snapshot from a local development supervisor. It never uses
// proxies, DNS, redirects, credentials, or a non-loopback connection.
func Inspect(ctx context.Context, authority string) (Inspection, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		return Inspection{}, fmt.Errorf("inspection address must be localhost or a loopback IP with a port")
	}
	number, err := strconv.Atoi(port)
	ip := net.ParseIP(host)
	if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port ||
		(!strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback())) {
		return Inspection{}, fmt.Errorf("inspection address must be localhost or a loopback IP with a port")
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		if !strings.EqualFold(host, "localhost") {
			return dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
		}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", port))
		if err == nil || ctx.Err() != nil {
			return conn, err
		}
		return dialer.DialContext(ctx, "tcp", net.JoinHostPort("::1", port))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+authority+InspectionPath, nil)
	if err != nil {
		return Inspection{}, fmt.Errorf("invalid inspection address")
	}
	r.Header.Set("Accept", "application/json")
	response, err := client.Do(r)
	if err != nil {
		return Inspection{}, fmt.Errorf("cannot reach local development inspection: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Inspection{}, fmt.Errorf("development inspection returned HTTP %d", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		return Inspection{}, fmt.Errorf("development inspection did not return JSON")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxInspectionBytes+1))
	if err != nil || len(data) > maxInspectionBytes {
		return Inspection{}, fmt.Errorf("development inspection response exceeds limits or is incomplete")
	}
	var result Inspection
	if err := json.Unmarshal(data, &result); err != nil || result.Version != inspectionVersion {
		return Inspection{}, fmt.Errorf("unsupported or invalid development inspection response")
	}
	return result, nil
}
