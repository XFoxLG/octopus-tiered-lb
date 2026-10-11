package relay

import (
	"net/http"
	"testing"
	"time"

	dbmodel "github.com/lingyuins/octopus/internal/model"
	ch "github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	inopenai "github.com/lingyuins/octopus/internal/transformer/inbound/openai"
	tmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestCanHedgeKeepsProtectedScenariosSerial(t *testing.T) {
	originalDisposableFunc := balancer.DisposableChannelFunc
	t.Cleanup(func() { balancer.DisposableChannelFunc = originalDisposableFunc })
	balancer.DisposableChannelFunc = func(channelID int) bool { return channelID == 31 }

	newTest := func(mutate func(group *dbmodel.Group, req *relayRequest, attempt *relayAttempt)) *relayAttempt {
		group := &dbmodel.Group{
			ID:           1,
			Mode:         dbmodel.GroupModeRoundRobin,
			HedgeEnabled: true,
			Items: []dbmodel.GroupItem{
				{ID: 1, GroupID: 1, ChannelID: 11, ModelName: "gpt-test"},
				{ID: 2, GroupID: 1, ChannelID: 21, ModelName: "gpt-test"},
			},
		}
		stream := true
		internalRequest := &tmodel.InternalLLMRequest{Model: "gpt-test", Stream: &stream}
		request := &relayRequest{
			group:           group,
			internalRequest: internalRequest,
			inAdapter:       &inopenai.ChatInbound{},
			hedgeRemaining:  2,
		}
		attempt := &relayAttempt{
			relayRequest: request,
			channel: &dbmodel.Channel{
				ID:      11,
				Name:    "primary",
				Enabled: true,
			},
			usedKey:     dbmodel.ChannelKey{ID: 111},
			adapterType: outbound.OutboundTypeOpenAIChat,
		}
		attempt.iter = balancer.NewIterator(*group, 7, "gpt-test", nil)
		if !attempt.iter.Next() {
			t.Fatal("iterator has no primary candidate")
		}
		if mutate != nil {
			mutate(group, request, attempt)
		}
		return attempt
	}

	tests := []struct {
		name   string
		mutate func(*dbmodel.Group, *relayRequest, *relayAttempt)
		want   bool
	}{
		{name: "eligible pair", mutate: nil, want: true},
		{
			name: "default disabled",
			mutate: func(group *dbmodel.Group, _ *relayRequest, _ *relayAttempt) {
				group.HedgeEnabled = false
			},
		},
		{
			name: "failover group",
			mutate: func(group *dbmodel.Group, _ *relayRequest, _ *relayAttempt) {
				group.Mode = dbmodel.GroupModeFailover
			},
		},
		{
			name: "sticky candidate",
			mutate: func(group *dbmodel.Group, _ *relayRequest, attempt *relayAttempt) {
				group.SessionKeepTime = 60
				balancer.SetSticky(7, "gpt-test", 11, 111, "gpt-test")
				t.Cleanup(func() { balancer.RemoveSticky(7, "gpt-test") })
				attempt.iter = balancer.NewIterator(*group, 7, "gpt-test", nil)
				if !attempt.iter.Next() {
					t.Fatal("iterator has no sticky candidate")
				}
			},
		},
		{
			name: "one-time channel in group",
			mutate: func(group *dbmodel.Group, _ *relayRequest, _ *relayAttempt) {
				group.Items = append(group.Items, dbmodel.GroupItem{
					ID: 3, GroupID: group.ID, ChannelID: 31, ModelName: "gpt-test",
				})
			},
		},
		{
			name: "disposable current candidate",
			mutate: func(_ *dbmodel.Group, _ *relayRequest, attempt *relayAttempt) {
				attempt.channel.Disposable = true
			},
		},
		{
			name: "non-streaming",
			mutate: func(_ *dbmodel.Group, request *relayRequest, _ *relayAttempt) {
				request.internalRequest.Stream = nil
			},
		},
		{
			name: "only one forwarded attempt allowed",
			mutate: func(_ *dbmodel.Group, request *relayRequest, _ *relayAttempt) {
				request.hedgeRemaining = 1
			},
		},
		{
			name: "unsupported outbound adapter",
			mutate: func(_ *dbmodel.Group, _ *relayRequest, attempt *relayAttempt) {
				attempt.adapterType = outbound.OutboundTypeGemini
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attempt := newTest(test.mutate)
			if got := attempt.canHedge(); got != test.want {
				t.Fatalf("canHedge() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCanHedgeHasNoCandidateForSingleCandidateGroup(t *testing.T) {
	stream := true
	group := dbmodel.Group{
		ID:           2,
		Mode:         dbmodel.GroupModeRoundRobin,
		HedgeEnabled: true,
		Items:        []dbmodel.GroupItem{{ID: 1, GroupID: 2, ChannelID: 41, ModelName: "gpt-test"}},
	}
	request := &relayRequest{
		group:           &group,
		internalRequest: &tmodel.InternalLLMRequest{Model: "gpt-test", Stream: &stream},
		inAdapter:       &inopenai.ChatInbound{},
		hedgeRemaining:  2,
	}
	attempt := &relayAttempt{
		relayRequest: request,
		channel:      &dbmodel.Channel{ID: 41, Name: "only", Enabled: true},
		usedKey:      dbmodel.ChannelKey{ID: 411},
		adapterType:  outbound.OutboundTypeOpenAIChat,
	}
	attempt.iter = balancer.NewIterator(group, 7, "gpt-test", nil)
	if !attempt.iter.Next() {
		t.Fatal("iterator setup failed")
	}
	if attempt.canHedge() {
		t.Fatal("single-candidate group must stay serial")
	}
}

func TestRecordUnselectedHedgeFailureSkipsCancelledLane(t *testing.T) {
	channelID, keyID, modelName := 987654, 987655, "hedge-cancel-model"
	channel := dbmodel.Channel{ID: channelID, Name: "cancelled-lane", CircuitBreakerThreshold: 1}
	ch.GetCache().Set(channelID, channel)
	t.Cleanup(func() {
		ch.GetCache().Del(channelID)
		balancer.RemoveChannelEntries(channelID)
	})

	lane := &relayAttempt{
		relayRequest: &relayRequest{
			internalRequest: &tmodel.InternalLLMRequest{Model: modelName},
		},
		channel: &channel,
		usedKey: dbmodel.ChannelKey{ID: keyID},
	}
	recordUnselectedHedgeFailure(lane, http.StatusBadGateway, errHedgeLost, time.Millisecond)

	if tripped, _ := balancer.IsTripped(channelID, keyID, modelName); tripped {
		t.Fatal("cancelled hedge lane must not update channel health")
	}
}
