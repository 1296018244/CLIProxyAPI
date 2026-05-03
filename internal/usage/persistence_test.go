package usage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadSnapshotFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	stats := NewRequestStatistics()
	stats.MergeSnapshot(StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
							Tokens:    TokenStats{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
						}},
					},
				},
			},
		},
	})

	if err := SaveSnapshotFile(path, stats); err != nil {
		t.Fatalf("SaveSnapshotFile() error = %v", err)
	}

	loaded := NewRequestStatistics()
	result, err := LoadSnapshotFile(path, loaded)
	if err != nil {
		t.Fatalf("LoadSnapshotFile() error = %v", err)
	}
	if result.Added != 1 || result.Skipped != 0 {
		t.Fatalf("load result = %+v, want added=1 skipped=0", result)
	}
	snapshot := loaded.Snapshot()
	if snapshot.TotalRequests != 1 || snapshot.TotalTokens != 3 {
		t.Fatalf("loaded totals = %d/%d, want 1/3", snapshot.TotalRequests, snapshot.TotalTokens)
	}
}

func TestLoadSnapshotFileMissingIsNoop(t *testing.T) {
	stats := NewRequestStatistics()
	result, err := LoadSnapshotFile(filepath.Join(t.TempDir(), "missing.json"), stats)
	if err != nil {
		t.Fatalf("LoadSnapshotFile() missing error = %v", err)
	}
	if result.Added != 0 || result.Skipped != 0 {
		t.Fatalf("missing result = %+v, want zero", result)
	}
}

func TestStartSnapshotPersistenceSavesOnCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	stats := NewRequestStatistics()
	stats.MergeSnapshot(StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
							Tokens:    TokenStats{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
						}},
					},
				},
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := StartSnapshotPersistence(ctx, path, stats, time.Hour)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("persistence did not stop after cancel")
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot file not written on cancel: %v", err)
	}
}
