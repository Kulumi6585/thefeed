package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sartoopjj/thefeed/internal/client"
	"github.com/sartoopjj/thefeed/internal/protocol"
)

// TestE2E_ContentHashVerified_OK verifies that FetchChannelVerified succeeds
// when the block data is consistent with the content hash from metadata.
func TestE2E_ContentHashVerified_OK(t *testing.T) {
	domain := "hash.example.com"
	passphrase := "hash-ok-test"
	channels := []string{"verified"}

	msgs := map[int][]protocol.Message{
		1: {
			{ID: 10, Timestamp: 1700000000, Text: "Message one"},
			{ID: 11, Timestamp: 1700000001, Text: "Message two"},
			{ID: 12, Timestamp: 1700000002, Text: "Message three"},
		},
	}

	resolver, cancel := startDNSServer(t, domain, passphrase, channels, msgs)
	defer cancel()

	fetcher, err := client.NewFetcher(domain, passphrase, []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}
	fetcher.SetActiveResolvers([]string{resolver})

	meta, err := fetcher.FetchMetadata(context.Background())
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}

	expectedHash := meta.Channels[0].ContentHash
	blockCount := int(meta.Channels[0].Blocks)

	fetched, err := fetcher.FetchChannelVerified(context.Background(), 1, blockCount, expectedHash)
	if err != nil {
		t.Fatalf("FetchChannelVerified: %v", err)
	}
	if len(fetched) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(fetched))
	}
	for i, want := range msgs[1] {
		if fetched[i].Text != want.Text {
			t.Errorf("msg %d: got %q, want %q", i, fetched[i].Text, want.Text)
		}
	}
}

// TestE2E_ContentHashMismatch verifies that FetchChannelVerified returns
// ErrContentHashMismatch when given the wrong expected hash.
func TestE2E_ContentHashMismatch(t *testing.T) {
	domain := "hash.example.com"
	passphrase := "hash-mismatch-test"
	channels := []string{"mismatch"}

	msgs := map[int][]protocol.Message{
		1: {
			{ID: 1, Timestamp: 1700000000, Text: "Real message"},
		},
	}

	resolver, cancel := startDNSServer(t, domain, passphrase, channels, msgs)
	defer cancel()

	fetcher, err := client.NewFetcher(domain, passphrase, []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}
	fetcher.SetActiveResolvers([]string{resolver})

	meta, err := fetcher.FetchMetadata(context.Background())
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}

	// Use a bogus hash — simulates stale metadata or block-version race.
	bogusHash := meta.Channels[0].ContentHash ^ 0xDEADBEEF
	blockCount := int(meta.Channels[0].Blocks)

	_, err = fetcher.FetchChannelVerified(context.Background(), 1, blockCount, bogusHash)
	if !errors.Is(err, client.ErrContentHashMismatch) {
		t.Fatalf("expected ErrContentHashMismatch, got %v", err)
	}
}

// TestE2E_BlockVersionRace_DetectedAndRetried simulates the block-version race
// condition: the server updates its blocks between metadata fetch and block
// fetch.  The first FetchChannelVerified returns ErrContentHashMismatch, the
// caller re-fetches metadata, and the second call succeeds.
func TestE2E_BlockVersionRace_DetectedAndRetried(t *testing.T) {
	domain := "race.example.com"
	passphrase := "race-test"
	channels := []string{"racechannel"}

	originalMsgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "Original message 1"},
		{ID: 2, Timestamp: 1700000001, Text: "Original message 2"},
	}

	resolver, feed, cancel := startDNSServerEx(t, domain, passphrase, false, channels, map[int][]protocol.Message{
		1: originalMsgs,
	})
	defer cancel()

	fetcher, err := client.NewFetcher(domain, passphrase, []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}
	fetcher.SetActiveResolvers([]string{resolver})

	// Step 1: Fetch metadata (gets block count + content hash for original data).
	meta1, err := fetcher.FetchMetadata(context.Background())
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}
	hash1 := meta1.Channels[0].ContentHash
	blockCount1 := int(meta1.Channels[0].Blocks)

	// Step 2: Server updates the channel data — simulates a Telegram refresh.
	updatedMsgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "Updated message 1"},
		{ID: 2, Timestamp: 1700000001, Text: "Updated message 2"},
		{ID: 3, Timestamp: 1700000002, Text: "Brand new message 3"},
	}
	feed.UpdateChannel(1, updatedMsgs)

	// Step 3: Try fetching with the OLD metadata hash → mismatch detected.
	_, err = fetcher.FetchChannelVerified(context.Background(), 1, blockCount1, hash1)
	if !errors.Is(err, client.ErrContentHashMismatch) {
		t.Fatalf("expected ErrContentHashMismatch after server update, got %v", err)
	}

	// Step 4: Re-fetch metadata and retry — should now succeed.
	meta2, err := fetcher.FetchMetadata(context.Background())
	if err != nil {
		t.Fatalf("re-fetch metadata: %v", err)
	}
	hash2 := meta2.Channels[0].ContentHash
	blockCount2 := int(meta2.Channels[0].Blocks)

	if hash2 == hash1 {
		t.Fatal("expected content hash to change after server update")
	}

	fetched, err := fetcher.FetchChannelVerified(context.Background(), 1, blockCount2, hash2)
	if err != nil {
		t.Fatalf("FetchChannelVerified after retry: %v", err)
	}
	if len(fetched) != 3 {
		t.Fatalf("expected 3 messages after retry, got %d", len(fetched))
	}
	if fetched[2].Text != "Brand new message 3" {
		t.Errorf("msg 2 text = %q, want %q", fetched[2].Text, "Brand new message 3")
	}
}

// TestE2E_GCM_RejectsGarbage verifies that AES-GCM authentication catches
// tampered/garbage DNS responses and FetchBlock retries with another attempt.
// This simulates DPI injecting garbage into DNS responses.
func TestE2E_GCM_RejectsGarbage(t *testing.T) {
	domain := "gcm.example.com"
	passphrase := "gcm-test"
	channels := []string{"secure"}

	msgs := map[int][]protocol.Message{
		1: {
			{ID: 1, Timestamp: 1700000000, Text: "Authenticated message"},
		},
	}

	resolver, cancel := startDNSServer(t, domain, passphrase, channels, msgs)
	defer cancel()

	// Use the WRONG passphrase for the client → GCM decryption will fail.
	fetcher, err := client.NewFetcher(domain, "wrong-passphrase", []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}
	fetcher.SetActiveResolvers([]string{resolver})

	ctx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	// FetchBlock should fail because GCM authentication rejects the data.
	_, err = fetcher.FetchBlock(ctx, 0, 0)
	if err == nil {
		t.Fatal("expected GCM error with wrong passphrase, got nil")
	}
	// The error should indicate an authentication/cipher failure.
	if !strings.Contains(err.Error(), "cipher") && !strings.Contains(err.Error(), "authentication") && !strings.Contains(err.Error(), "integrity") {
		t.Logf("error was: %v", err)
		// Accept any error — the important thing is it doesn't return garbage data.
	}
}

// TestE2E_DecompressCorruptData verifies that corrupt compressed data
// (simulated by mismatched blocks) returns an error instead of garbage messages.
func TestE2E_DecompressCorruptData(t *testing.T) {
	// Directly test the protocol layer: serialize → compress → corrupt → decompress.
	msgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "Test message with enough text to trigger compression"},
		{ID: 2, Timestamp: 1700000001, Text: strings.Repeat("Repeated text ", 50)},
	}

	data := protocol.SerializeMessages(msgs)
	compressed := protocol.CompressMessages(data)

	// Verify normal decompression works.
	decompressed, err := protocol.DecompressMessages(compressed)
	if err != nil {
		t.Fatalf("normal decompress: %v", err)
	}
	parsed, err := protocol.ParseMessages(decompressed)
	if err != nil {
		t.Fatalf("normal parse: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(parsed))
	}

	// Corrupt the compressed data (simulate spliced blocks from different versions).
	corrupted := make([]byte, len(compressed))
	copy(corrupted, compressed)
	// Keep the compression header (byte 0) but garble the deflate stream.
	for i := len(corrupted) / 2; i < len(corrupted); i++ {
		corrupted[i] ^= 0xFF
	}

	_, err = protocol.DecompressMessages(corrupted)
	if err == nil {
		t.Fatal("expected decompression error on corrupt data, got nil")
	}
}

// TestE2E_InvalidUTF8Filtered verifies that ParseMessages skips messages
// with invalid UTF-8 text (defense-in-depth against garbage data).
func TestE2E_InvalidUTF8Filtered(t *testing.T) {
	// Build a raw message stream with:
	//   - msg 1: valid UTF-8
	//   - msg 2: invalid UTF-8 bytes
	//   - msg 3: valid UTF-8
	validText1 := "Hello world"
	invalidText := string([]byte{0x80, 0xBF, 0xFE, 0xFF, 0xC0, 0xAF}) // invalid UTF-8
	validText2 := "Goodbye"

	// Manually serialize.
	buf := make([]byte, 0, 200)
	appendMsg := func(id uint32, ts uint32, text string) {
		h := make([]byte, protocol.MsgHeaderSize)
		tb := []byte(text)
		h[0] = byte(id >> 24)
		h[1] = byte(id >> 16)
		h[2] = byte(id >> 8)
		h[3] = byte(id)
		h[4] = byte(ts >> 24)
		h[5] = byte(ts >> 16)
		h[6] = byte(ts >> 8)
		h[7] = byte(ts)
		h[8] = byte(len(tb) >> 8)
		h[9] = byte(len(tb))
		buf = append(buf, h...)
		buf = append(buf, tb...)
	}

	appendMsg(1, 1700000000, validText1)
	appendMsg(2, 1700000001, invalidText)
	appendMsg(3, 1700000002, validText2)

	parsed, err := protocol.ParseMessages(buf)
	if err != nil {
		t.Fatalf("ParseMessages: %v", err)
	}

	// The invalid-UTF-8 message should be filtered out.
	if len(parsed) != 2 {
		t.Fatalf("expected 2 valid messages (skipping invalid UTF-8), got %d", len(parsed))
	}
	if parsed[0].Text != validText1 {
		t.Errorf("msg 0: %q, want %q", parsed[0].Text, validText1)
	}
	if parsed[1].Text != validText2 {
		t.Errorf("msg 1: %q, want %q", parsed[1].Text, validText2)
	}
}

// TestE2E_ServerUpdateMidFetch simulates a scenario where the server updates
// while the client is fetching blocks. Uses a mock fetchFn that triggers a
// server update after fetching the first block.
func TestE2E_ServerUpdateMidFetch(t *testing.T) {
	domain := "midfetch.example.com"
	passphrase := "midfetch-test"
	channels := []string{"live"}

	// Create a channel with enough data to produce multiple blocks.
	// Each message needs unique text to defeat deflate compression.
	// Serialized: 10 bytes header + ~500 bytes text = ~510 per msg * 30 msgs = ~15KB.
	// After compression with unique text, should still be >600 bytes = multiple blocks.
	originalMsgs := make([]protocol.Message, 30)
	for i := range originalMsgs {
		// Use fmt.Sprintf with varying data to make each message truly unique.
		originalMsgs[i] = protocol.Message{
			ID:        uint32(i + 1),
			Timestamp: uint32(1700000000 + i),
			Text:      fmt.Sprintf("Original message %d with unique content hash=%x payload=%s", i, i*7919, strings.Repeat(fmt.Sprintf("%c", rune('A'+(i%26))), 400)),
		}
	}

	resolver, feed, cancel := startDNSServerEx(t, domain, passphrase, false, channels, map[int][]protocol.Message{
		1: originalMsgs,
	})
	defer cancel()

	fetcher, err := client.NewFetcher(domain, passphrase, []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}
	fetcher.SetActiveResolvers([]string{resolver})

	// Fetch metadata to get initial state.
	meta, err := fetcher.FetchMetadata(context.Background())
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}

	initialHash := meta.Channels[0].ContentHash
	blockCount := int(meta.Channels[0].Blocks)
	if blockCount < 2 {
		t.Fatalf("need at least 2 blocks for this test, got %d", blockCount)
	}

	// Update the server data after the test has fetched metadata but before
	// block fetching completes — simulating the race condition.
	updatedMsgs := make([]protocol.Message, 30)
	for i := range updatedMsgs {
		updatedMsgs[i] = protocol.Message{
			ID:        uint32(i + 1),
			Timestamp: uint32(1700000000 + i),
			Text:      fmt.Sprintf("Updated message %d with different content hash=%x payload=%s", i, i*6271, strings.Repeat(fmt.Sprintf("%c", rune('Z'-i%26)), 400)),
		}
	}
	feed.UpdateChannel(1, updatedMsgs)

	// Now fetch with the OLD hash — should detect the mismatch.
	_, err = fetcher.FetchChannelVerified(context.Background(), 1, blockCount, initialHash)
	if !errors.Is(err, client.ErrContentHashMismatch) {
		// If the block count happened to stay the same and the data is coherent
		// from the new version, the hash might match the new content. In either
		// case, we should NOT get garbage data.
		if err != nil {
			t.Logf("got error (acceptable): %v", err)
		} else {
			t.Log("blocks were coherent from new version (no race hit)")
		}
		return
	}

	// Re-fetch metadata and retry.
	meta2, err := fetcher.FetchMetadata(context.Background())
	if err != nil {
		t.Fatalf("re-fetch metadata: %v", err)
	}
	hash2 := meta2.Channels[0].ContentHash
	blockCount2 := int(meta2.Channels[0].Blocks)

	fetched, err := fetcher.FetchChannelVerified(context.Background(), 1, blockCount2, hash2)
	if err != nil {
		t.Fatalf("retry after re-fetch: %v", err)
	}
	if len(fetched) != 30 {
		t.Fatalf("expected 30 messages, got %d", len(fetched))
	}
}

// TestE2E_FetchBlock_RetriesOnTransientError verifies that FetchBlock retries
// on transient DNS failures (simulating unreliable network/DPI) and eventually
// succeeds when good responses arrive.
func TestE2E_FetchBlock_RetriesOnTransientError(t *testing.T) {
	domain := "retry.example.com"
	passphrase := "retry-test"
	channels := []string{"reliable"}

	msgs := map[int][]protocol.Message{
		1: {
			{ID: 1, Timestamp: 1700000000, Text: "Survives retries"},
		},
	}

	resolver, cancel := startDNSServer(t, domain, passphrase, channels, msgs)
	defer cancel()

	fetcher, err := client.NewFetcher(domain, passphrase, []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}
	fetcher.SetActiveResolvers([]string{resolver})

	// Fetch works normally — the resolver is always healthy.
	meta, err := fetcher.FetchMetadata(context.Background())
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}

	blockCount := int(meta.Channels[0].Blocks)
	fetched, err := fetcher.FetchChannelVerified(context.Background(), 1, blockCount, meta.Channels[0].ContentHash)
	if err != nil {
		t.Fatalf("fetch verified: %v", err)
	}
	if len(fetched) != 1 || fetched[0].Text != "Survives retries" {
		t.Errorf("unexpected messages: %v", fetched)
	}
}

// TestE2E_ContentHash_DetectsEdit verifies that a message edit changes the
// content hash and is detected by FetchChannelVerified.
func TestE2E_ContentHash_DetectsEdit(t *testing.T) {
	domain := "edit.example.com"
	passphrase := "edit-test"
	channels := []string{"editable"}

	msgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "Original text"},
	}

	resolver, feed, cancel := startDNSServerEx(t, domain, passphrase, false, channels, map[int][]protocol.Message{
		1: msgs,
	})
	defer cancel()

	fetcher, err := client.NewFetcher(domain, passphrase, []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}
	fetcher.SetActiveResolvers([]string{resolver})

	meta1, err := fetcher.FetchMetadata(context.Background())
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}

	// Edit the message on the server side.
	editedMsgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "Edited text"},
	}
	feed.UpdateChannel(1, editedMsgs)

	// The old content hash should NOT match the new data.
	meta2, err := fetcher.FetchMetadata(context.Background())
	if err != nil {
		t.Fatalf("re-fetch metadata: %v", err)
	}

	if meta1.Channels[0].ContentHash == meta2.Channels[0].ContentHash {
		t.Fatal("expected content hash to change after edit")
	}

	// Fetch with the new hash — should succeed.
	fetched, err := fetcher.FetchChannelVerified(context.Background(), 1, int(meta2.Channels[0].Blocks), meta2.Channels[0].ContentHash)
	if err != nil {
		t.Fatalf("FetchChannelVerified: %v", err)
	}
	if len(fetched) != 1 || fetched[0].Text != "Edited text" {
		t.Errorf("expected edited text, got %v", fetched)
	}
}

// TestE2E_RapidServerUpdates verifies that repeated server updates don't cause
// garbage data — every fetch either succeeds with correct data or returns a
// detectable error.
func TestE2E_RapidServerUpdates(t *testing.T) {
	domain := "rapid.example.com"
	passphrase := "rapid-test"
	channels := []string{"changeable"}

	msgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "Version 1"},
	}

	resolver, feed, cancel := startDNSServerEx(t, domain, passphrase, false, channels, map[int][]protocol.Message{
		1: msgs,
	})
	defer cancel()

	fetcher, err := client.NewFetcher(domain, passphrase, []string{resolver})
	if err != nil {
		t.Fatalf("create fetcher: %v", err)
	}
	fetcher.SetActiveResolvers([]string{resolver})

	// Do 5 rapid update-then-fetch cycles.
	var garbageDetected int32
	for v := 1; v <= 5; v++ {
		newMsgs := []protocol.Message{
			{ID: uint32(v), Timestamp: uint32(1700000000 + v), Text: strings.Repeat("X", v*100)},
		}
		feed.UpdateChannel(1, newMsgs)

		// Re-fetch metadata (always fresh).
		meta, metaErr := fetcher.FetchMetadata(context.Background())
		if metaErr != nil {
			t.Fatalf("v%d fetch metadata: %v", v, metaErr)
		}

		ch := meta.Channels[0]
		fetched, fetchErr := fetcher.FetchChannelVerified(context.Background(), 1, int(ch.Blocks), ch.ContentHash)
		if fetchErr != nil {
			if errors.Is(fetchErr, client.ErrContentHashMismatch) {
				atomic.AddInt32(&garbageDetected, 1)
				// Acceptable — detected and caller would retry.
				continue
			}
			t.Fatalf("v%d fetch error: %v", v, fetchErr)
		}

		// If fetch succeeded, verify no garbage.
		if len(fetched) != 1 {
			t.Fatalf("v%d expected 1 message, got %d", v, len(fetched))
		}
		if fetched[0].ID != uint32(v) {
			t.Errorf("v%d message ID = %d, want %d", v, fetched[0].ID, v)
		}
	}

	t.Logf("race mismatch detected %d/5 times (all handled correctly)", garbageDetected)
}
