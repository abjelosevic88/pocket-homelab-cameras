package capture

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// tsPacket builds a 188-byte TS packet: pid, payload-unit-start, an
// adaptation field with random access when rai, then payload.
func tsPacket(pid int, pusi, rai bool, payload []byte) []byte {
	p := make([]byte, 0, tsSize)
	b1 := byte(pid>>8) & 0x1f
	if pusi {
		b1 |= 0x40
	}
	p = append(p, 0x47, b1, byte(pid))
	if rai {
		p = append(p, 0x30, 1, 0x40) // adaptation field + payload, 1 byte of flags: random access
	} else {
		p = append(p, 0x10)
	}
	p = append(p, payload...)
	for len(p) < tsSize {
		p = append(p, 0xff)
	}
	return p[:tsSize]
}

func psi(tableID byte, body []byte) []byte {
	l := len(body) + 5 + 4 // after section_length: ext(2) version(1) sec(1) last(1) body crc(4)
	s := []byte{0x00, tableID, 0xb0 | byte(l>>8), byte(l), 0x00, 0x01, 0xc1, 0x00, 0x00}
	s = append(s, body...)
	return append(s, 0, 0, 0, 0) // CRC (not checked)
}

func patPacket(pmtPID int) []byte {
	return tsPacket(0, true, false, psi(0x00, []byte{0x00, 0x01, 0xe0 | byte(pmtPID>>8), byte(pmtPID)}))
}

func pmtPacket(pmtPID int, streamType byte, videoPID int) []byte {
	body := []byte{0xe0 | byte(videoPID>>8), byte(videoPID), 0xf0, 0x00, // PCR PID, no program info
		0x0f, 0xe1, 0x01, 0xf0, 0x00, // AAC on 0x101
		streamType, 0xe0 | byte(videoPID>>8), byte(videoPID), 0xf0, 0x00}
	return tsPacket(pmtPID, true, false, psi(0x02, body))
}

func TestTables(t *testing.T) {
	if got := patPMT(patPacket(0x1000)); got != 0x1000 {
		t.Fatalf("pmt pid = %#x", got)
	}
	for typ, want := range map[byte]string{0x24: "hevc", 0x1b: "h264"} {
		pid, codec := pmtVideo(pmtPacket(0x1000, typ, 0x100))
		if pid != 0x100 || codec != want {
			t.Fatalf("type %#x: pid %#x codec %q", typ, pid, codec)
		}
	}
	if !randomAccess(tsPacket(0x100, true, true, nil)) || randomAccess(tsPacket(0x100, true, false, nil)) {
		t.Fatal("random access flag")
	}
}

func TestRelayStartsReadersAtKeyFrame(t *testing.T) {
	r := &relay{url: "test", subs: map[*RelaySub]struct{}{}, pmtPID: -1, videoPID: -1, ready: make(chan struct{}), cancel: func() {}}
	relaysMu.Lock()
	relays["test"] = r
	relaysMu.Unlock()
	defer func() { relaysMu.Lock(); delete(relays, "test"); relaysMu.Unlock() }()

	pat, pmt := patPacket(0x1000), pmtPacket(0x1000, 0x24, 0x100)
	old := tsPacket(0x100, true, false, []byte("old"))
	key := tsPacket(0x100, true, true, []byte("key"))
	next := tsPacket(0x100, false, false, []byte("next"))
	r.feed(bytes.Join([][]byte{pat, pmt, old, key, next}, nil))

	select {
	case <-r.ready:
	default:
		t.Fatal("codec not known after the PMT")
	}
	if r.codec != "hevc" {
		t.Fatalf("codec %q", r.codec)
	}

	s := Subscribe("test")
	live := tsPacket(0x100, false, false, []byte("live"))
	r.feed(live)
	want := bytes.Join([][]byte{pat, pmt, key, next, live}, nil)
	got := make([]byte, len(want))
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(s, got); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader got nothing")
	}
	if !bytes.Equal(got, want) {
		t.Fatal("a joining reader must get the tables, then everything from the newest key frame, then live packets")
	}
	s.Close()
	if s.CameraErr() != nil {
		t.Fatal("leaving is not the camera's trouble")
	}
	r.mu.Lock()
	idle := r.idle != nil
	r.mu.Unlock()
	if !idle {
		t.Fatal("the last reader leaving should start the linger timer")
	}
	r.idle.Stop()
}
