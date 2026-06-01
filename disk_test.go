package dawg

import (
	"bytes"
	"testing"
)

// TestVarintRoundTrip checks the 7-bit "7code" varint at and around each byte
// boundary: every byte holds 7 value bits, so a single byte must encode 0..127.
func TestVarintRoundTrip(t *testing.T) {
	values := []uint64{
		0, 1,
		0x7e, 0x7f, 0x80, // 1 vs 2 byte boundary
		0x3ffe, 0x3fff, 0x4000, // 2 vs 3 byte boundary
		0x1ffffe, 0x1fffff, 0x200000, // 3 vs 4 byte boundary
		0xffffffe, 0xfffffff, // top of the 4-byte range
	}

	for _, v := range values {
		var buf bytes.Buffer
		w := newBitWriter(&buf)
		writeUnsigned(w, v)
		w.Flush()

		got := buf.Bytes()
		if uint64(len(got)) != unsignedLength(v) {
			t.Errorf("v=%#x: wrote %d bytes but unsignedLength=%d", v, len(got), unsignedLength(v))
		}

		r := newBitSeeker(bytes.NewReader(got))
		if back := readUnsigned(&r); back != v {
			t.Errorf("v=%#x: round-trip returned %#x", v, back)
		}
	}
}

// TestVarintBoundaryIsOneByte locks the fix that 0x7f encodes in a single byte.
func TestVarintBoundaryIsOneByte(t *testing.T) {
	if got := unsignedLength(0x7f); got != 1 {
		t.Errorf("unsignedLength(0x7f)=%d, want 1", got)
	}
	var buf bytes.Buffer
	w := newBitWriter(&buf)
	writeUnsigned(w, 0x7f)
	w.Flush()
	if got := buf.Len(); got != 1 {
		t.Errorf("writeUnsigned(0x7f) wrote %d bytes, want 1", got)
	}
}
