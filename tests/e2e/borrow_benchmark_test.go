package e2e_test

import (
	"io"
	"testing"
	"time"
)

// BenchmarkBorrowRead measures the complete authorized view with real storage.
// Stream consumers discard bytes; Connect consumers validate the decoded Quote.
func BenchmarkBorrowRead(b *testing.B) {
	for _, name := range []string{"stream", "connect"} {
		b.Run(
			name,
			func(b *testing.B) {
				b.StopTimer()
				s := &scenario{
					root: b.TempDir(),
					ctx:  b.Context(),
				}
				s.clock.Store(time.Now().UnixNano())
				b.Cleanup(
					func() {
						if err := s.stopPeer(); err != nil {
							b.Error(err)
						}
					},
				)
				p := &peerContentScenario{
					s: s,
				}
				if err := p.setup(); err != nil {
					b.Fatal(err)
				}
				if err := s.borrow(); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				b.StartTimer()
				for range b.N {
					if name == "stream" {
						reader, err := s.service.Read(s.ctx, "borrower", s.decision.Lease.Id)
						if err != nil {
							b.Fatal(err)
						}
						n, err := io.Copy(io.Discard, reader)
						if err != nil || n <= 4<<20 {
							b.Fatalf("stream bytes %d, error %v", n, err)
						}
						continue
					}
					quote, err := s.client.ReadView(s.ctx, s.decision.Lease)
					if err != nil {
						b.Fatal(err)
					}
					if len(quote.Entries) != 4 {
						b.Fatal("incomplete Quote")
					}
				}
			},
		)
	}
}
