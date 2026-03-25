package protocol

import (
	"encoding/binary"
	"fmt"
)

const (
	// DefaultBlockPayload is the decrypted payload per DNS TXT block.
	// Calculated to stay within 512-byte UDP DNS limit after encryption + base64 + padding overhead.
	DefaultBlockPayload = 180

	// DefaultMaxPadding is the default random padding added to responses to vary DNS response size.
	DefaultMaxPadding = 32

	// PadLengthSize is the 2-byte length prefix added before real data when padding is used.
	PadLengthSize = 2

	// MetadataChannel is the special channel number for server metadata.
	MetadataChannel = 0

	// MarkerSize is the random marker in metadata to verify data freshness.
	MarkerSize = 3

	// Query payload structure sizes.
	QueryPaddingSize = 4
	QueryChannelSize = 2
	QueryBlockSize   = 2
	QueryPayloadSize = QueryPaddingSize + QueryChannelSize + QueryBlockSize // 8

	// Message header sizes (in the serialized message stream).
	MsgIDSize        = 4
	MsgTimestampSize = 4
	MsgLengthSize    = 2
	MsgHeaderSize    = MsgIDSize + MsgTimestampSize + MsgLengthSize // 10
)

// Media placeholder strings for non-text content.
const (
	MediaImage    = "[IMAGE]"
	MediaVideo    = "[VIDEO]"
	MediaFile     = "[FILE]"
	MediaAudio    = "[AUDIO]"
	MediaSticker  = "[STICKER]"
	MediaGIF      = "[GIF]"
	MediaPoll     = "[POLL]"
	MediaContact  = "[CONTACT]"
	MediaLocation = "[LOCATION]"
)

// Metadata holds channel 0 data: server info + channel list.
type Metadata struct {
	Marker    [MarkerSize]byte
	Timestamp uint32
	Channels  []ChannelInfo
}

// ChannelInfo describes a single feed channel.
type ChannelInfo struct {
	Name      string
	Blocks    uint16
	LastMsgID uint32
}

// Message represents a single feed message in a channel.
type Message struct {
	ID        uint32
	Timestamp uint32
	Text      string
}

// SerializeMetadata encodes metadata into bytes for channel 0 blocks.
func SerializeMetadata(m *Metadata) []byte {
	// 3 marker + 4 timestamp + 2 channel count + per-channel data
	size := MarkerSize + 4 + 2
	for _, ch := range m.Channels {
		size += 1 + len(ch.Name) + 2 + 4
	}
	buf := make([]byte, size)
	off := 0

	copy(buf[off:], m.Marker[:])
	off += MarkerSize

	binary.BigEndian.PutUint32(buf[off:], m.Timestamp)
	off += 4

	binary.BigEndian.PutUint16(buf[off:], uint16(len(m.Channels)))
	off += 2

	for _, ch := range m.Channels {
		nameBytes := []byte(ch.Name)
		if len(nameBytes) > 255 {
			nameBytes = nameBytes[:255]
		}
		buf[off] = byte(len(nameBytes))
		off++
		copy(buf[off:], nameBytes)
		off += len(nameBytes)
		binary.BigEndian.PutUint16(buf[off:], ch.Blocks)
		off += 2
		binary.BigEndian.PutUint32(buf[off:], ch.LastMsgID)
		off += 4
	}

	return buf
}

// ParseMetadata decodes metadata from concatenated channel 0 block data.
func ParseMetadata(data []byte) (*Metadata, error) {
	if len(data) < MarkerSize+4+2 {
		return nil, fmt.Errorf("metadata too short: %d bytes", len(data))
	}
	m := &Metadata{}
	off := 0

	copy(m.Marker[:], data[off:off+MarkerSize])
	off += MarkerSize

	m.Timestamp = binary.BigEndian.Uint32(data[off:])
	off += 4

	count := binary.BigEndian.Uint16(data[off:])
	off += 2

	m.Channels = make([]ChannelInfo, 0, count)
	for i := 0; i < int(count); i++ {
		if off >= len(data) {
			return nil, fmt.Errorf("truncated metadata at channel %d", i)
		}
		nameLen := int(data[off])
		off++
		if off+nameLen > len(data) {
			return nil, fmt.Errorf("truncated channel name at %d", i)
		}
		name := string(data[off : off+nameLen])
		off += nameLen

		if off+6 > len(data) {
			return nil, fmt.Errorf("truncated channel info at %d", i)
		}
		blocks := binary.BigEndian.Uint16(data[off:])
		off += 2
		lastID := binary.BigEndian.Uint32(data[off:])
		off += 4

		m.Channels = append(m.Channels, ChannelInfo{
			Name:      name,
			Blocks:    blocks,
			LastMsgID: lastID,
		})
	}

	return m, nil
}

// SerializeMessages encodes messages into a byte stream for data channel blocks.
func SerializeMessages(msgs []Message) []byte {
	size := 0
	for _, msg := range msgs {
		size += MsgHeaderSize + len(msg.Text)
	}
	buf := make([]byte, size)
	off := 0

	for _, msg := range msgs {
		textBytes := []byte(msg.Text)
		binary.BigEndian.PutUint32(buf[off:], msg.ID)
		off += MsgIDSize
		binary.BigEndian.PutUint32(buf[off:], msg.Timestamp)
		off += MsgTimestampSize
		binary.BigEndian.PutUint16(buf[off:], uint16(len(textBytes)))
		off += MsgLengthSize
		copy(buf[off:], textBytes)
		off += len(textBytes)
	}

	return buf
}

// ParseMessages decodes messages from concatenated data channel block data.
func ParseMessages(data []byte) ([]Message, error) {
	var msgs []Message
	off := 0

	for off < len(data) {
		if off+MsgHeaderSize > len(data) {
			break // incomplete message header, stop
		}
		id := binary.BigEndian.Uint32(data[off:])
		off += MsgIDSize
		ts := binary.BigEndian.Uint32(data[off:])
		off += MsgTimestampSize
		textLen := int(binary.BigEndian.Uint16(data[off:]))
		off += MsgLengthSize

		if off+textLen > len(data) {
			break // incomplete message text, stop
		}
		text := string(data[off : off+textLen])
		off += textLen

		msgs = append(msgs, Message{
			ID:        id,
			Timestamp: ts,
			Text:      text,
		})
	}

	return msgs, nil
}

// SplitIntoBlocks splits data into blocks of DefaultBlockPayload size.
func SplitIntoBlocks(data []byte) [][]byte {
	if len(data) == 0 {
		return [][]byte{{}}
	}
	var blocks [][]byte
	for i := 0; i < len(data); i += DefaultBlockPayload {
		end := i + DefaultBlockPayload
		if end > len(data) {
			end = len(data)
		}
		block := make([]byte, end-i)
		copy(block, data[i:end])
		blocks = append(blocks, block)
	}
	return blocks
}
