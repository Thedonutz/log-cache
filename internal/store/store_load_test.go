package store_test

import (
	"fmt"
	"sync"
	"time"

	"code.cloudfoundry.org/go-loggregator/rpc/loggregator_v2"
	"code.cloudfoundry.org/log-cache/internal/store"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("store under high concurrent load", func() {
	timeoutInSeconds := 300

	FIt("", func(done Done) {
		var wg sync.WaitGroup

		sp := newSpyPruner()
		sp.result = 100
		sm := newSpyMetrics()

		loadStore := store.NewStore(2500, 4000, sp, sm)
		start := time.Now()

		// 10 writers per sourceId, 10k envelopes per writer
		var sourceId, writers, envelopes int
		for sourceId = 0; sourceId < 10; sourceId++ {
			for writers = 0; writers < 10; writers++ {
				wg.Add(1)
				go func(sourceId string) {
					defer wg.Done()

					for envelopes = 0; envelopes < 10000; envelopes++ {
						e := buildTypedEnvelope(time.Now().UnixNano(), sourceId, &loggregator_v2.Log{})
						loadStore.Put(e, sourceId)
						time.Sleep(10 * time.Microsecond)
					}
				}(fmt.Sprintf("index-%d", sourceId))
			}
		}

		for readers := 0; readers < 10; readers++ {
			go func() {
				for i := 0; i < 100; i++ {
					loadStore.Meta()
					time.Sleep(200 * time.Millisecond)
				}
			}()
		}

		go func() {
			wg.Wait()
			fmt.Printf("Finished writing %d  envelopes in %s\n", sourceId*writers*envelopes, time.Since(start))
			close(done)
		}()

		Consistently(func() int64 {
			envelopes := loadStore.Get("index-9", start, time.Now(), nil, 100000, false)
			fmt.Println("GetCount() =", loadStore.GetCount(), "CountEnvelopes() =", loadStore.CountEnvelopes())
			return int64(len(envelopes))
		}, timeoutInSeconds).Should(BeNumerically("<=", 10000))

	}, float64(timeoutInSeconds))
})
