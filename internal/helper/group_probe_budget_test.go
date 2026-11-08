package helper

import (
	"context"
	"testing"
	"time"

	m "github.com/lingyuins/octopus/internal/model"
)

func TestGroupProbeUsesEarliestEffectiveBudget(t *testing.T) {
	for _, tc := range []struct {
		name         string
		group        m.Group
		channel      m.Channel
		parent, want time.Duration
	}{
		{name: "default", want: 25 * time.Second},
		{name: "draft request", group: m.Group{RequestTimeoutSeconds: 3}, want: 3 * time.Second},
		{name: "draft attempt", group: m.Group{AttemptTimeOut: 2}, want: 2 * time.Second},
		{name: "draft first output", group: m.Group{FirstTokenTimeOut: 1}, want: time.Second},
		{name: "channel override", group: m.Group{AttemptTimeOut: 2}, channel: m.Channel{AttemptTimeOut: 5}, want: 5 * time.Second},
		{name: "parent", group: m.Group{RequestTimeoutSeconds: 3}, parent: 200 * time.Millisecond, want: 200 * time.Millisecond},
		{name: "ceiling", group: m.Group{RequestTimeoutSeconds: 900}, want: 25 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.parent > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.parent)
				defer cancel()
			}
			probe, cancel := groupProbeContext(ctx, &tc.group, &tc.channel)
			defer cancel()
			deadline, ok := probe.Deadline()
			left := time.Until(deadline)
			if !ok || left > tc.want || left < tc.want-100*time.Millisecond {
				t.Fatalf("deadline=%v want=%v", left, tc.want)
			}
		})
	}
}
