package server

import (
	"testing"
	"time"

	"github.com/sartoopjj/thefeed/internal/protocol"
)

// recordReportQuery routes events to the right counter on hourlyFetchReport.
// Each subtest exercises one branch.
func TestRecordReportQuery(t *testing.T) {
	cases := []struct {
		name  string
		event reportEvent
		check func(t *testing.T, rep *hourlyFetchReport)
	}{
		{
			name:  "invalid increments only invalidQueries",
			event: reportEvent{invalid: true, channel: 999, resolver: "1.1.1.1:53"},
			check: func(t *testing.T, rep *hourlyFetchReport) {
				if rep.invalidQueries != 1 {
					t.Errorf("invalidQueries = %d, want 1", rep.invalidQueries)
				}
				if rep.totalQueries != 0 {
					t.Errorf("totalQueries = %d, want 0 (invalid must not count toward total)", rep.totalQueries)
				}
				if len(rep.perResolver) != 0 {
					t.Errorf("perResolver populated for invalid event: %v", rep.perResolver)
				}
				if rep.metadataQueries != 0 || rep.mediaQueries != 0 || rep.versionQueries != 0 {
					t.Errorf("invalid event leaked into another counter: %+v", rep)
				}
			},
		},
		{
			name:  "metadata channel",
			event: reportEvent{channel: protocol.MetadataChannel, resolver: "1.1.1.1:53"},
			check: func(t *testing.T, rep *hourlyFetchReport) {
				if rep.totalQueries != 1 {
					t.Errorf("totalQueries = %d, want 1", rep.totalQueries)
				}
				if rep.metadataQueries != 1 {
					t.Errorf("metadataQueries = %d, want 1", rep.metadataQueries)
				}
				if rep.invalidQueries != 0 {
					t.Errorf("invalidQueries = %d, want 0", rep.invalidQueries)
				}
				if rep.perResolver["1.1.1.1:53"] != 1 {
					t.Errorf("perResolver missing entry: %v", rep.perResolver)
				}
			},
		},
		{
			name:  "version channel",
			event: reportEvent{channel: protocol.VersionChannel},
			check: func(t *testing.T, rep *hourlyFetchReport) {
				if rep.versionQueries != 1 {
					t.Errorf("versionQueries = %d, want 1", rep.versionQueries)
				}
			},
		},
		{
			name:  "media channel",
			event: reportEvent{channel: protocol.MediaChannelStart + 5},
			check: func(t *testing.T, rep *hourlyFetchReport) {
				if rep.mediaQueries != 1 {
					t.Errorf("mediaQueries = %d, want 1", rep.mediaQueries)
				}
				if len(rep.perChannel) != 0 {
					t.Errorf("media events should not populate perChannel: %v", rep.perChannel)
				}
			},
		},
		{
			name:  "regular content channel",
			event: reportEvent{channel: 5},
			check: func(t *testing.T, rep *hourlyFetchReport) {
				if rep.totalQueries != 1 {
					t.Errorf("totalQueries = %d, want 1", rep.totalQueries)
				}
				stats := rep.perChannel[5]
				if stats == nil || stats.Queries != 1 {
					t.Errorf("perChannel[5] = %+v, want Queries=1", stats)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := newHourlyFetchReport(time.Now())
			recordReportQuery(rep, tc.event)
			tc.check(t, rep)
		})
	}
}

// Many invalid events shouldn't bleed into other counters. This is the
// common-case failure mode the counter exists to track (clients
// over-fetching past the last block of a channel).
func TestRecordReportQueryInvalidVolume(t *testing.T) {
	rep := newHourlyFetchReport(time.Now())
	for i := 0; i < 1000; i++ {
		recordReportQuery(rep, reportEvent{invalid: true, channel: uint16(i % 65535)})
	}
	if rep.invalidQueries != 1000 {
		t.Errorf("invalidQueries = %d, want 1000", rep.invalidQueries)
	}
	if rep.totalQueries != 0 {
		t.Errorf("totalQueries = %d, want 0 — invalid must never inflate the legit-query total", rep.totalQueries)
	}
	if len(rep.perChannel) != 0 || len(rep.perResolver) != 0 {
		t.Errorf("invalid events polluted perChannel or perResolver maps: ch=%d res=%d",
			len(rep.perChannel), len(rep.perResolver))
	}
}

// Mixed valid and invalid events must each hit only their own counter.
func TestRecordReportQueryMixed(t *testing.T) {
	rep := newHourlyFetchReport(time.Now())
	recordReportQuery(rep, reportEvent{channel: protocol.MetadataChannel, resolver: "1.1.1.1:53"})
	recordReportQuery(rep, reportEvent{channel: 3, resolver: "1.1.1.1:53"})
	recordReportQuery(rep, reportEvent{channel: protocol.MediaChannelStart + 1})
	recordReportQuery(rep, reportEvent{invalid: true})
	recordReportQuery(rep, reportEvent{invalid: true})

	if rep.totalQueries != 3 {
		t.Errorf("totalQueries = %d, want 3 (3 valid events)", rep.totalQueries)
	}
	if rep.invalidQueries != 2 {
		t.Errorf("invalidQueries = %d, want 2", rep.invalidQueries)
	}
	if rep.metadataQueries != 1 {
		t.Errorf("metadataQueries = %d, want 1", rep.metadataQueries)
	}
	if rep.mediaQueries != 1 {
		t.Errorf("mediaQueries = %d, want 1", rep.mediaQueries)
	}
	if rep.perChannel[3] == nil || rep.perChannel[3].Queries != 1 {
		t.Errorf("perChannel[3] missing or wrong: %+v", rep.perChannel[3])
	}
}
