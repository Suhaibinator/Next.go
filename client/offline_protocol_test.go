package client

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func offlineProtocolFixture() (offlineRequest, offlineReply) {
	return offlineRequest{Version: 1, Command: "status", OwnerPolicyID: "reader", RuntimeID: strings.Repeat("a", 20), ConfigurationID: strings.Repeat("b", 64), RequestID: "offline-1"}, offlineReply{Version: 1, RequestID: "offline-1", OwnerPolicyID: "reader", RuntimeID: strings.Repeat("c", 20), ConfigurationID: strings.Repeat("d", 64), Generation: strings.Repeat("e", 64), ActiveGeneration: strings.Repeat("e", 64), State: "ready", Enabled: true, Supported: true, SavedAtUnix: 100, HighestObservedUnix: 101}
}

func TestOfflineStatusBindsActualWorkerBeforeMutation(t *testing.T) {
	request, reply := offlineProtocolFixture()
	raw, _ := json.Marshal(reply)
	actual, err := decodeOfflineReply(raw, request)
	if err != nil || actual.RuntimeID != reply.RuntimeID || actual.ConfigurationID != reply.ConfigurationID {
		t.Fatalf("old active worker status: %+v, %v", actual, err)
	}
	request.Command = "disable"
	if _, err := decodeOfflineReply(raw, request); !errors.Is(err, ErrOfflineProtocol) {
		t.Fatal("a mutation accepted a different worker identity")
	}
	request.RuntimeID, request.ConfigurationID = actual.RuntimeID, actual.ConfigurationID
	reply.State, reply.Enabled, reply.DeferredCleanup = "disabled", false, true
	raw, _ = json.Marshal(reply)
	confirmed, err := decodeOfflineReply(raw, request)
	if err != nil || !confirmed.DeferredCleanup || confirmed.ActiveGeneration == "" || confirmed.Enabled {
		t.Fatalf("deferred cleanup lost retained ownership: %+v, %v", confirmed, err)
	}
}

func TestOfflineReplyRejectsUnboundOrMalformedControlData(t *testing.T) {
	request, good := offlineProtocolFixture()
	cases := []struct {
		name string
		edit func(*offlineReply)
	}{
		{"version", func(r *offlineReply) { r.Version = 2 }},
		{"request", func(r *offlineReply) { r.RequestID = "offline-2" }},
		{"owner", func(r *offlineReply) { r.OwnerPolicyID = "other" }},
		{"runtime", func(r *offlineReply) { r.RuntimeID = strings.Repeat("A", 20) }},
		{"configuration", func(r *offlineReply) { r.ConfigurationID = "invalid" }},
		{"generation", func(r *offlineReply) { r.Generation = strings.Repeat("e", 65) }},
		{"previous", func(r *offlineReply) { r.PreviousGeneration = "../foreign" }},
		{"state", func(r *offlineReply) { r.State = "activating" }},
		{"ready-disabled", func(r *offlineReply) { r.Enabled = false }},
		{"ready-unsupported", func(r *offlineReply) { r.Supported = false }},
		{"ready-empty", func(r *offlineReply) { r.ActiveGeneration = "" }},
		{"disabled-enabled", func(r *offlineReply) { r.State = "disabled" }},
		{"negative-time", func(r *offlineReply) { r.SavedAtUnix = -1 }},
		{"unsafe-time", func(r *offlineReply) { r.HighestObservedUnix = 1 << 53 }},
		{"public-error", func(r *offlineReply) { r.Error = "private error detail\n" }},
		{"noncanonical-error", func(r *offlineReply) { r.Error = "Private_Error" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			reply := good
			test.edit(&reply)
			raw, _ := json.Marshal(reply)
			if _, err := decodeOfflineReply(raw, request); !errors.Is(err, ErrOfflineProtocol) {
				t.Fatalf("accepted malformed reply: %s (%v)", raw, err)
			}
		})
	}
	raw, _ := json.Marshal(good)
	for name, invalid := range map[string][]byte{
		"missing":   []byte(strings.Replace(string(raw), `,"deferredCleanup":false`, "", 1)),
		"duplicate": []byte(strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1)),
		"alias":     []byte(strings.Replace(string(raw), `"version"`, `"Version"`, 1)),
		"unknown":   []byte(strings.Replace(string(raw), `"version":1`, `"version":1,"extra":true`, 1)),
		"null":      []byte(strings.Replace(string(raw), `"error":""`, `"error":null`, 1)),
		"trailing":  append(append([]byte{}, raw...), []byte(` {}`)...),
		"oversize":  []byte(strings.Repeat(" ", offlineReplyLimit+1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeOfflineReply(invalid, request); !errors.Is(err, ErrOfflineProtocol) {
				t.Fatalf("accepted invalid wire shape: %v", err)
			}
		})
	}
}
