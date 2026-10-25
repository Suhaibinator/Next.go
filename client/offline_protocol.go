package client

import (
	"encoding/json"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/ascii"
	"github.com/Suhaibinator/Next.go/internal/offlinejson"
)

const offlineMessageVersion = 1
const offlineReplyLimit = 16 << 10

// Status discovers an already-owned worker's actual compatibility identity.
// Later mutating commands bind that identity rather than a possibly newer page.
type offlineRequest struct {
	Version         int    `json:"version"`
	Command         string `json:"command"`
	OwnerPolicyID   string `json:"ownerPolicyID"`
	RuntimeID       string `json:"runtimeID"`
	ConfigurationID string `json:"configurationID"`
	RequestID       string `json:"requestID"`
}

type offlineReply struct {
	Version             int    `json:"version"`
	RequestID           string `json:"requestID"`
	OwnerPolicyID       string `json:"ownerPolicyID"`
	RuntimeID           string `json:"runtimeID"`
	ConfigurationID     string `json:"configurationID"`
	Generation          string `json:"generation"`
	ActiveGeneration    string `json:"activeGeneration"`
	PreviousGeneration  string `json:"previousGeneration"`
	StagingGeneration   string `json:"stagingGeneration"`
	State               string `json:"state"`
	Enabled             bool   `json:"enabled"`
	DeferredCleanup     bool   `json:"deferredCleanup"`
	Supported           bool   `json:"supported"`
	SavedAtUnix         int64  `json:"savedAtUnix"`
	HighestObservedUnix int64  `json:"highestObservedUnix"`
	Error               string `json:"error"`
}

func decodeOfflineReply(raw []byte, request offlineRequest) (offlineReply, error) {
	var reply offlineReply
	if len(raw) == 0 || len(raw) > offlineReplyLimit || offlinejson.Decode(raw, &reply) != nil {
		return reply, ErrOfflineProtocol
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 16 {
		return offlineReply{}, ErrOfflineProtocol
	}
	if reply.Version != offlineMessageVersion || reply.RequestID != request.RequestID || reply.OwnerPolicyID != request.OwnerPolicyID || !ascii.LowerHex(reply.RuntimeID, 20) || !ascii.LowerHex(reply.ConfigurationID, 64) {
		return offlineReply{}, ErrOfflineProtocol
	}
	if request.Command != "status" && (reply.RuntimeID != request.RuntimeID || reply.ConfigurationID != request.ConfigurationID) {
		return offlineReply{}, ErrOfflineProtocol
	}
	for _, generation := range []string{reply.Generation, reply.ActiveGeneration, reply.PreviousGeneration, reply.StagingGeneration} {
		if generation != "" && !ascii.LowerHex(generation, 64) {
			return offlineReply{}, ErrOfflineProtocol
		}
	}
	if reply.SavedAtUnix < 0 || reply.HighestObservedUnix < 0 || reply.SavedAtUnix > 1<<53-1 || reply.HighestObservedUnix > 1<<53-1 || len(reply.Error) > 64 || strings.IndexFunc(reply.Error, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-')
	}) >= 0 {
		return offlineReply{}, ErrOfflineProtocol
	}
	switch reply.State {
	case "disabled", "ready", "staged", "failed", "busy":
	default:
		return offlineReply{}, ErrOfflineProtocol
	}
	if reply.State == "ready" && (!reply.Enabled || !reply.Supported || reply.ActiveGeneration == "") || reply.State == "disabled" && reply.Enabled {
		return offlineReply{}, ErrOfflineProtocol
	}
	return reply, nil
}
