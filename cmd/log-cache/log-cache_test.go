package main_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"code.cloudfoundry.org/go-loggregator/rpc/loggregator_v2"
	"code.cloudfoundry.org/log-cache/internal/store"
)

var StoreSize = 1000000

func benchmarkDBStore(testSize int, b *testing.B) {
	s := store.NewStore(testSize, StoreSize, &staticPruner{}, &nopMetrics{})
	defer s.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for n := 0; n < testSize; n++ {
			s.PutDB(&loggregator_v2.Envelope{
				Timestamp: time.Now().UnixNano(),
			}, "source-id")
		}
	}
}

func BenchmarkDBStore1(b *testing.B)    { benchmarkDBStore(1, b) }
func BenchmarkDBStore10(b *testing.B)   { benchmarkDBStore(10, b) }
func BenchmarkDBStore100(b *testing.B)  { benchmarkDBStore(100, b) }
func BenchmarkDBStore1k(b *testing.B)   { benchmarkDBStore(1000, b) }
func BenchmarkDBStore10k(b *testing.B)  { benchmarkDBStore(10000, b) }
func BenchmarkDBStore100k(b *testing.B) { benchmarkDBStore(100000, b) }
func BenchmarkDBStore1M(b *testing.B)   { benchmarkDBStore(1000000, b) }

func benchmarkDBStoreBatch(testSize int, b *testing.B) {
	s := store.NewStore(testSize, StoreSize, &staticPruner{}, &nopMetrics{})
	defer s.Close()

	var es []*loggregator_v2.Envelope
	for n := 0; n < testSize; n++ {
		es = append(es, &loggregator_v2.Envelope{
			Timestamp: time.Now().UnixNano(),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.PutDBBatch(es)
	}
}

func BenchmarkDBStoreBatch1(b *testing.B)    { benchmarkDBStoreBatch(1, b) }
func BenchmarkDBStoreBatch10(b *testing.B)   { benchmarkDBStoreBatch(10, b) }
func BenchmarkDBStoreBatch100(b *testing.B)  { benchmarkDBStoreBatch(100, b) }
func BenchmarkDBStoreBatch1k(b *testing.B)   { benchmarkDBStoreBatch(1000, b) }
func BenchmarkDBStoreBatch10k(b *testing.B)  { benchmarkDBStoreBatch(10000, b) }
func BenchmarkDBStoreBatch100k(b *testing.B) { benchmarkDBStoreBatch(100000, b) }
func BenchmarkDBStoreBatch1M(b *testing.B)   { benchmarkDBStoreBatch(1000000, b) }

func benchmarkDBStoreGet(testSize int, b *testing.B) {
	err := os.RemoveAll("/home/pivotal/workspace/log-cache-release/db")
	if err != nil {
		panic(err)
	}

	s := store.NewStore(testSize, StoreSize, &staticPruner{}, &nopMetrics{})
	defer s.Close()

	var es []*loggregator_v2.Envelope
	for n := 0; n < testSize; n++ {
		es = append(es, &loggregator_v2.Envelope{
			SourceId:  "source-id",
			Timestamp: time.Now().UnixNano(),
		})
	}

	s.PutDBBatch(es)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.GetDB("source-id", 1000)
	}
}

func BenchmarkDBStoreGet1k(b *testing.B)   { benchmarkDBStoreGet(1000, b) }
func BenchmarkDBStoreGet10k(b *testing.B)  { benchmarkDBStoreGet(10000, b) }
func BenchmarkDBStoreGet100k(b *testing.B) { benchmarkDBStoreGet(100000, b) }
func BenchmarkDBStoreGet1M(b *testing.B)   { benchmarkDBStoreGet(1000000, b) }

func benchmarkDBStoreConcurrent(testSize int, b *testing.B) {
	err := os.RemoveAll("/home/pivotal/workspace/log-cache-release/db")
	if err != nil {
		panic(err)
	}

	s := store.NewStore(testSize, StoreSize, &staticPruner{}, &nopMetrics{})
	defer s.Close()

	doneChan := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-doneChan:
				return
			default:
				s.Put(&loggregator_v2.Envelope{
					Timestamp: time.Now().UnixNano(),
				}, "source-id-2")
			}
		}
	}()

	var es []*loggregator_v2.Envelope
	for n := 0; n < testSize; n++ {
		es = append(es, &loggregator_v2.Envelope{
			Timestamp: time.Now().UnixNano(),
			SourceId:  "source-id",
		})
	}
	s.PutDBBatch(es)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.GetDB("source-id", 1000)
	}
	close(doneChan)
}

func BenchmarkDBStoreConcurrent1k(b *testing.B)   { benchmarkDBStoreConcurrent(1000, b) }
func BenchmarkDBStoreConcurrent10k(b *testing.B)  { benchmarkDBStoreConcurrent(10000, b) }
func BenchmarkDBStoreConcurrent100k(b *testing.B) { benchmarkDBStoreConcurrent(100000, b) }
func BenchmarkDBStoreConcurrent1M(b *testing.B)   { benchmarkDBStoreConcurrent(1000000, b) }

func BenchmarkStore(b *testing.B) {
	s := store.NewStore(100000, StoreSize, &staticPruner{}, &nopMetrics{})
	defer s.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for n := 0; n < 100000; n++ {
			s.Put(&loggregator_v2.Envelope{
				Timestamp: time.Now().UnixNano(),
			}, "source-id")
		}
	}
}

func benchmarkStoreGetConcurrent(testSize int, b *testing.B) {
	s := store.NewStore(testSize, StoreSize, &staticPruner{}, &nopMetrics{})
	defer s.Close()

	var i uint64
	doneChan := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-doneChan:
				return
			default:
				s.Put(&loggregator_v2.Envelope{
					Timestamp: time.Now().UnixNano(),
				}, fmt.Sprintf("source-id-%d", i%10000))
			}
			i += 1
		}
	}()

	var es []*loggregator_v2.Envelope
	for n := 0; n < testSize; n++ {
		es = append(es, &loggregator_v2.Envelope{
			Timestamp: time.Now().UnixNano(),
		})
	}

	for _, e := range es {
		s.Put(e, "source-id")
	}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		s.Get("source-id", time.Time{}, time.Now(), nil, 1000, false)
	}
	b.StopTimer()

	close(doneChan)
}

func BenchmarkStoreGetConcurrent1k(b *testing.B)   { benchmarkStoreGetConcurrent(1000, b) }
func BenchmarkStoreGetConcurrent10k(b *testing.B)  { benchmarkStoreGetConcurrent(10000, b) }
func BenchmarkStoreGetConcurrent100k(b *testing.B) { benchmarkStoreGetConcurrent(100000, b) }
func BenchmarkStoreGetConcurrent1M(b *testing.B)   { benchmarkStoreGetConcurrent(1000000, b) }

func benchmarkStoreGet(testSize int, b *testing.B) {
	s := store.NewStore(testSize, StoreSize, &staticPruner{}, &nopMetrics{}, false)

	for n := 0; n < testSize; n++ {
		s.Put(&loggregator_v2.Envelope{
			SourceId:  "source-id",
			Timestamp: time.Now().UnixNano(),
		}, "source-id")
	}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		s.Get("source-id", time.Time{}, time.Now(), nil, 1000, false)
	}
	b.StopTimer()
}

func BenchmarkStoreGet1k(b *testing.B)   { benchmarkStoreGet(1000, b) }
func BenchmarkStoreGet10k(b *testing.B)  { benchmarkStoreGet(10000, b) }
func BenchmarkStoreGet100k(b *testing.B) { benchmarkStoreGet(100000, b) }
func BenchmarkStoreGet1M(b *testing.B)   { benchmarkStoreGet(1000000, b) }

type nopMetrics struct{}

func (n nopMetrics) NewCounter(string) func(delta uint64) {
	return func(uint64) {}
}

func (n nopMetrics) NewGauge(string) func(value float64) {
	return func(float64) {}
}

type staticPruner struct {
	size int
}

func (s *staticPruner) Prune() int {
	s.size++
	if s.size > StoreSize {
		return s.size - StoreSize
	}

	return 0
}
