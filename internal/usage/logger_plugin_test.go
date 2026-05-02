package usage

import (
	"context"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestRequestStatisticsRecordIncludesLatencyAndTokens(t *testing.T) {
	prev := StatisticsEnabled()
	SetStatisticsEnabled(true)
	t.Cleanup(func() { SetStatisticsEnabled(prev) })

	stats := NewRequestStatistics()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		Source:      "user@example.com",
		AuthIndex:   "2",
		RequestedAt: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
		Latency:     1500 * time.Millisecond,
		Detail: coreusage.Detail{
			InputTokens:     10,
			OutputTokens:    20,
			ReasoningTokens: 3,
			CachedTokens:    4,
			TotalTokens:     37,
		},
	})

	snapshot := stats.Snapshot()
	if snapshot.TotalRequests != 1 {
		t.Fatalf("total requests = %d, want 1", snapshot.TotalRequests)
	}
	if snapshot.SuccessCount != 1 {
		t.Fatalf("success count = %d, want 1", snapshot.SuccessCount)
	}
	if snapshot.TotalTokens != 37 {
		t.Fatalf("total tokens = %d, want 37", snapshot.TotalTokens)
	}

	apiSnapshot := snapshot.APIs["test-key"]
	modelSnapshot := apiSnapshot.Models["gpt-5.4"]
	if modelSnapshot.TotalRequests != 1 {
		t.Fatalf("model requests = %d, want 1", modelSnapshot.TotalRequests)
	}
	if len(modelSnapshot.Details) != 1 {
		t.Fatalf("details len = %d, want 1", len(modelSnapshot.Details))
	}

	detail := modelSnapshot.Details[0]
	if detail.LatencyMs != 1500 {
		t.Fatalf("latency_ms = %d, want 1500", detail.LatencyMs)
	}
	if detail.Source != "user@example.com" || detail.AuthIndex != "2" {
		t.Fatalf("source/auth index = %q/%q, want user@example.com/2", detail.Source, detail.AuthIndex)
	}
	if detail.Tokens.InputTokens != 10 || detail.Tokens.OutputTokens != 20 || detail.Tokens.ReasoningTokens != 3 || detail.Tokens.CachedTokens != 4 || detail.Tokens.TotalTokens != 37 {
		t.Fatalf("unexpected tokens: %+v", detail.Tokens)
	}
}

func TestRequestStatisticsRecordRespectsDisabledSwitch(t *testing.T) {
	prev := StatisticsEnabled()
	SetStatisticsEnabled(false)
	t.Cleanup(func() { SetStatisticsEnabled(prev) })

	stats := NewRequestStatistics()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		RequestedAt: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
		Detail:      coreusage.Detail{InputTokens: 1, OutputTokens: 2},
	})

	snapshot := stats.Snapshot()
	if snapshot.TotalRequests != 0 {
		t.Fatalf("total requests = %d, want 0", snapshot.TotalRequests)
	}
}

func TestRequestStatisticsMergeSnapshotDeduplicatesDetails(t *testing.T) {
	prev := StatisticsEnabled()
	SetStatisticsEnabled(true)
	t.Cleanup(func() { SetStatisticsEnabled(prev) })

	stats := NewRequestStatistics()
	timestamp := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	snapshot := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 2500,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}

	first := stats.MergeSnapshot(snapshot)
	if first.Added != 1 || first.Skipped != 0 {
		t.Fatalf("first merge = %+v, want added=1 skipped=0", first)
	}
	second := stats.MergeSnapshot(snapshot)
	if second.Added != 0 || second.Skipped != 1 {
		t.Fatalf("second merge = %+v, want added=0 skipped=1", second)
	}
	got := stats.Snapshot()
	if got.TotalRequests != 1 || got.TotalTokens != 30 {
		t.Fatalf("snapshot totals = requests %d tokens %d, want 1/30", got.TotalRequests, got.TotalTokens)
	}
}
