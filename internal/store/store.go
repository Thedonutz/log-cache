package store

import (
	"sync"
	"sync/atomic"
	"time"

	"code.cloudfoundry.org/go-log-cache/rpc/logcache_v1"
	"code.cloudfoundry.org/go-loggregator/rpc/loggregator_v2"
	"github.com/emirpasic/gods/trees/avltree"
	"github.com/emirpasic/gods/utils"
)

// Metrics is the client used for initializing metrics.
type Metrics interface {
	NewCounter(name string) func(delta uint64)
	NewGauge(name string) func(value float64)
}

// Pruner is used to determine if the store should prune.
type Pruner interface {
	// Prune returns the number of envelopes to prune.
	Prune() int
}

type LockableTree struct {
	meta logcache_v1.MetaInfo

	*avltree.Tree
	sync.RWMutex
}

// Store is an in-memory data store for envelopes. It will store envelopes up
// to a per-source threshold and evict oldest data first, as instructed by the
// Pruner. All functions are thread safe.
type Store struct {
	// TODO - indexes is overloaded
	indexes sync.Map

	// expirationIndex stores a reference to each tree index by its oldest
	// timestamp to facilitate pruning.
	expirationIndex *treeIndex

	// count is incremented/decremented atomically during Put. Pruning occurs
	// only when count surpasses minimumStoreSizeToPrune
	count                   int64
	minimumStoreSizeToPrune int

	maxPerSource int

	// metrics
	// TODO - this owuld be more clear if we namespaced it
	incExpired     func(delta uint64)
	setCachePeriod func(value float64)
	incIngress     func(delta uint64)
	incEgress      func(delta uint64)
	setStoreSize   func(value float64)

	p Pruner
}

func NewStore(maxPerSource, minimumStoreSizeToPrune int, p Pruner, m Metrics) *Store {
	return &Store{
		expirationIndex: newTreeIndex(),

		minimumStoreSizeToPrune: minimumStoreSizeToPrune,
		maxPerSource:            maxPerSource,

		incExpired:     m.NewCounter("Expired"),
		setCachePeriod: m.NewGauge("CachePeriod"),
		incIngress:     m.NewCounter("Ingress"),
		incEgress:      m.NewCounter("Egress"),
		setStoreSize:   m.NewGauge("StoreSize"),

		p: p,
	}
}

func (store *Store) Put(envelope *loggregator_v2.Envelope, index string) {
	store.incIngress(1)

	// Hold a lock on the expirationIndex while we check to see if this
	// will require creation of a new tree
	store.expirationIndex.Lock()
	// TODO: Can we use LoadOrStore to clean this up?
	tree, ok := store.indexes.Load(index)

	if !ok {
		newTree := &LockableTree{Tree: avltree.NewWith(utils.Int64Comparator)}
		store.indexes.Store(index, newTree)
		store.expirationIndex.PutTree(envelope.Timestamp, newTree)
		tree = newTree
	}
	store.expirationIndex.Unlock()

	tree.(*LockableTree).Lock()

	var oldestBeforeInsertion int64
	preexistingTree := tree.(*LockableTree).Size() > 0
	if preexistingTree {
		oldestBeforeInsertion = tree.(*LockableTree).Left().Key.(int64)
	}

	treeSizeBeforeInsertion := tree.(*LockableTree).Size()
	if treeSizeBeforeInsertion >= store.maxPerSource {
		tree.(*LockableTree).Remove(oldestBeforeInsertion)

		store.incExpired(1)
		tree.(*LockableTree).meta.Expired++
	}

	tree.(*LockableTree).Put(envelope.Timestamp, envelopeWrapper{e: envelope, index: index})

	// Only increment if we didn't overwrite.
	sizeDiff := int64(tree.(*LockableTree).Size() - treeSizeBeforeInsertion)
	atomic.AddInt64(&store.count, sizeDiff)

	oldestAfterInsertion := tree.(*LockableTree).Left().Key.(int64)
	tree.(*LockableTree).Unlock()

	if oldestBeforeInsertion != oldestAfterInsertion && preexistingTree {
		// TODO - this seems like it could be extracted to a method
		store.expirationIndex.Lock()
		store.expirationIndex.RemoveTree(oldestBeforeInsertion, tree.(*LockableTree))
		store.expirationIndex.PutTree(oldestAfterInsertion, tree.(*LockableTree))
		store.expirationIndex.Unlock()
	}

	store.truncate()
	store.setStoreSize(float64(atomic.LoadInt64(&store.count)))

	tree.(*LockableTree).Lock()
	if envelope.GetTimestamp() > tree.(*LockableTree).meta.NewestTimestamp {
		tree.(*LockableTree).meta.NewestTimestamp = envelope.GetTimestamp()
	}

	// TODO - this logic maybe belongs in truncate() - Put() seems like an odd place
	// 		to consider that the tree has been deleted during truncate()
	if tree.(*LockableTree).Size() > 0 {
		tree.(*LockableTree).meta.OldestTimestamp = tree.(*LockableTree).Left().Key.(int64)
	}
	tree.(*LockableTree).Unlock()

	// TODO - probably a helper
	oldestValue, oldestTree := store.expirationIndex.LeftTree()
	if oldestTree != nil {
		cachePeriod := (time.Now().UnixNano() - oldestValue) / int64(time.Millisecond)
		store.setCachePeriod(float64(cachePeriod))
	}
}

// truncate removes the n oldest envelopes across all trees
func (store *Store) truncate() {
	numberToPrune := int64(store.p.Prune())
	storeCount := atomic.LoadInt64(&store.count)
	minimumStoreSizeToPrune := int64(store.minimumStoreSizeToPrune)

	// Make sure we don't prune below the minimum size
	if storeCount-numberToPrune < minimumStoreSizeToPrune {
		numberToPrune = storeCount - minimumStoreSizeToPrune
		// TODO - may want to eject here instead of starting the loop with a
		//   negative numberToPrune
	}

	for i := int64(0); i < numberToPrune; i++ {
		store.removeOldestEnvelope()
	}
}

func (store *Store) removeOldestEnvelope() {
	store.expirationIndex.Lock()

	oldestTimestamp, treeToPrune := store.expirationIndex.RemoveLeftTree()

	treeToPrune.Lock()
	defer treeToPrune.Unlock()

	atomic.AddInt64(&store.count, -1)
	store.incExpired(1)

	oldestEnvelope := treeToPrune.Left()
	// TODO: maybe move this index to the LockableTree
	treeIndex := oldestEnvelope.Value.(envelopeWrapper).index

	treeToPrune.Remove(oldestTimestamp)

	if treeToPrune.Size() == 0 {
		store.indexes.Delete(treeIndex)
		store.expirationIndex.Unlock()
		return
	}

	// TODO - can we extract a function for 'update meta and expirationIndex?'
	oldestAfterRemoval := treeToPrune.Left().Key.(int64)
	store.expirationIndex.PutTree(oldestAfterRemoval, treeToPrune)
	store.expirationIndex.Unlock()

	treeToPrune.meta.Expired++
	treeToPrune.meta.OldestTimestamp = oldestAfterRemoval
}

// Get fetches envelopes from the store based on the source ID, start and end
// time. Start is inclusive while end is not: [start..end).
func (store *Store) Get(
	index string,
	start time.Time,
	end time.Time,
	envelopeTypes []logcache_v1.EnvelopeType,
	limit int,
	descending bool,
) []*loggregator_v2.Envelope {
	tree, ok := store.indexes.Load(index)
	if !ok {
		return nil
	}

	tree.(*LockableTree).RLock()
	defer tree.(*LockableTree).RUnlock()

	traverser := store.treeAscTraverse
	if descending {
		traverser = store.treeDescTraverse
	}

	var res []*loggregator_v2.Envelope
	traverser(tree.(*LockableTree).Root, start.UnixNano(), end.UnixNano(), func(e *loggregator_v2.Envelope, idx string) bool {
		if idx == index && store.validEnvelopeType(e, envelopeTypes) {
			res = append(res, e)
		}

		// Return true to stop traversing
		return len(res) >= limit
	})

	store.incEgress(uint64(len(res)))
	return res
}

func (s *Store) validEnvelopeType(e *loggregator_v2.Envelope, types []logcache_v1.EnvelopeType) bool {
	if types == nil {
		return true
	}
	for _, t := range types {
		if s.checkEnvelopeType(e, t) {
			return true
		}
	}
	return false
}

func (s *Store) treeAscTraverse(
	n *avltree.Node,
	start int64,
	end int64,
	f func(e *loggregator_v2.Envelope, index string) bool,
) bool {
	if n == nil {
		return false
	}

	t := n.Key.(int64)
	if t >= start {
		if s.treeAscTraverse(n.Children[0], start, end, f) {
			return true
		}

		w := n.Value.(envelopeWrapper)

		if t >= end || f(w.e, w.index) {
			return true
		}
	}

	return s.treeAscTraverse(n.Children[1], start, end, f)
}

func (s *Store) treeDescTraverse(
	n *avltree.Node,
	start int64,
	end int64,
	f func(e *loggregator_v2.Envelope, index string) bool,
) bool {
	if n == nil {
		return false
	}

	t := n.Key.(int64)
	if t < end {
		if s.treeDescTraverse(n.Children[1], start, end, f) {
			return true
		}

		w := n.Value.(envelopeWrapper)

		if t < start || f(w.e, w.index) {
			return true
		}
	}

	return s.treeDescTraverse(n.Children[0], start, end, f)
}

func (s *Store) checkEnvelopeType(e *loggregator_v2.Envelope, t logcache_v1.EnvelopeType) bool {
	if t == logcache_v1.EnvelopeType_ANY {
		return true
	}

	switch t {
	case logcache_v1.EnvelopeType_LOG:
		return e.GetLog() != nil
	case logcache_v1.EnvelopeType_COUNTER:
		return e.GetCounter() != nil
	case logcache_v1.EnvelopeType_GAUGE:
		return e.GetGauge() != nil
	case logcache_v1.EnvelopeType_TIMER:
		return e.GetTimer() != nil
	case logcache_v1.EnvelopeType_EVENT:
		return e.GetEvent() != nil
	default:
		// This should never happen. This implies the store is being used
		// poorly.
		panic("unknown type")
	}
}

// Meta returns each source ID tracked in the store.
func (store *Store) Meta() map[string]logcache_v1.MetaInfo {
	metaReport := make(map[string]logcache_v1.MetaInfo)

	store.indexes.Range(func(index interface{}, tree interface{}) bool {
		tree.(*LockableTree).RLock()
		metaReport[index.(string)] = tree.(*LockableTree).meta
		tree.(*LockableTree).RUnlock()

		return true
	})

	// Range over our local copy of metaReport
	// TODO - shouldn't we just maintain Count on metaReport..?!
	for index, meta := range metaReport {
		tree, _ := store.indexes.Load(index)

		tree.(*LockableTree).RLock()
		meta.Count = int64(tree.(*LockableTree).Size())
		tree.(*LockableTree).RUnlock()
		metaReport[index] = meta
	}
	return metaReport
}

// TODO - choose a coherent naming strategy for this. Either go with
// expiration and be specific, or this is just any index
type treeIndex struct {
	*avltree.Tree
	sync.RWMutex
}

func newTreeIndex() *treeIndex {
	return &treeIndex{
		Tree: avltree.NewWith(utils.Int64Comparator),
	}
}

func (index *treeIndex) PutTree(key int64, treeToIndex *LockableTree) {
	var values []*LockableTree
	if existing, found := index.Get(key); found {
		values = existing.([]*LockableTree)
	}

	index.Put(key, append(values, treeToIndex))
}

func (index *treeIndex) RemoveTree(key int64, treeToIndex *LockableTree) {
	var values []*LockableTree
	if existing, found := index.Get(key); found {
		values = existing.([]*LockableTree)
	}

	for i, v := range values {
		if v == treeToIndex {
			values = append(values[:i], values[i+1:]...)
			break
		}
	}

	if len(values) == 0 {
		index.Remove(key)
		return
	}

	index.Put(key, values)
}

// TODO: maybe perhaps consider wrtiting a test for this at some point. maybe.
func (index *treeIndex) RemoveLeftTree() (int64, *LockableTree) {
	l := index.Left()
	timestamp, values := l.Key.(int64), l.Value.([]*LockableTree)

	lt := values[0]
	values = values[1:]

	if len(values) == 0 {
		index.Remove(timestamp)
		return timestamp, lt
	}

	index.Put(timestamp, values)
	return timestamp, lt
}

func (index *treeIndex) LeftTree() (int64, *LockableTree) {
	index.RLock()
	defer index.RUnlock()

	if index.Size() == 0 {
		return 0, nil
	}

	l := index.Left()
	return l.Key.(int64), l.Value.([]*LockableTree)[0]
}

type envelopeWrapper struct {
	e     *loggregator_v2.Envelope
	index string
}
