package forge

import (
	"context"
	"sync"
	"time"
)

// Result is one backend's outcome, delivered the moment that backend is
// resolved rather than after every backend is.
type Result struct {
	// Index is the backend's position in the tools slice that was passed to
	// DetectStream. It is what lets a caller reassemble the list in the original
	// preference order no matter what order the results arrive in.
	Index int
	// Candidate is the resolved backend.
	Candidate Candidate
}

// DetectStream probes every tool concurrently and delivers each result as soon
// as that tool is ready, closing the channel once they have all arrived.
//
// Results arrive in completion order, which is not the order the user cares
// about — they care that fj is usable and is therefore shown first, while cafe,
// which is still doing its TLS handshake, is simply not there yet. The Index on
// each result is what preserves the original ordering for anything that needs it.
//
// An error is returned only for a bad tool name, which is a usage error worth
// reporting before any process is spawned. A backend that fails to probe is
// reported in its own Candidate, never here: one broken CLI must not hide the
// four that work.
func DetectStream(ctx context.Context, tools []Tool, opts ...Option) (<-chan Result, error) {
	// Discovery is pure and cannot fail for a known tool, so do it up front: a
	// bad tool name is a usage error and must be reported before any process is
	// spawned.
	backends := make([]Backend, len(tools))
	for i, t := range tools {
		b, err := Discover(t, opts...)
		if err != nil {
			return nil, err
		}
		backends[i] = b
	}
	return detectStream(ctx, backends), nil
}

// detectStream is DetectStream for an already-built list of backends. Splitting it
// out is what lets the ordering and delivery behaviour be tested against
// backends that finish when the test says they do, instead of against real CLIs
// on a real network.
func detectStream(ctx context.Context, backends []Backend) <-chan Result {
	file := readCache()
	ttl := detectTTL
	// fresh starts as a copy rather than an alias. cacheFile holds a map, so
	// assigning the struct copies the map *header*, not the map: storing into
	// fresh would otherwise write to the very map every other backend's lookup is
	// reading, and Go maps are not safe for concurrent use — that is a panic, not
	// just a wrong answer. Copying up front also means file stays immutable for
	// the whole run, which is what lets lookup stay lock-free.
	fresh := file.clone()

	var (
		mu    sync.Mutex
		dirty bool
	)

	// Buffered for every result, so a slow backend can never block a fast one
	// from being handed to the caller. That is the entire point of streaming:
	// fj finishing in five milliseconds must not sit in a queue behind cafe.
	ch := make(chan Result, len(backends))
	var wg sync.WaitGroup
	for i, b := range backends {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Every backend goes through the same path, memoised or not. Sending
			// cache hits inline instead would deliver them before any probe had a
			// chance to finish, so a warm cafe would always arrive ahead of a live
			// fj — and the display would settle rows in an order that has nothing
			// to do with how long each one took.
			c, cached := cachedOrProbe(ctx, b, file, ttl)
			if !cached {
				mu.Lock()
				fresh = fresh.store(b, c.detection())
				dirty = true
				mu.Unlock()
			}
			ch <- Result{Index: i, Candidate: c}
		}()
	}

	go func() {
		wg.Wait()
		// Before closing, deliberately: closing is what tells the caller it is done,
		// and the caller then prints and exits. Writing afterwards loses the race
		// against process exit, and the memo silently never lands — which looks
		// exactly like the memo not working.
		//
		// Keyed off whether anything was actually probed, rather than off whether the
		// entry count changed. Re-probing a backend whose entry already exists
		// replaces that entry without changing how many there are, so a length
		// comparison never noticed: the memo kept its old timestamp, went stale
		// again immediately, and a newly installed backend's key never reached disk
		// at all.
		mu.Lock()
		written := fresh
		needsWrite := dirty
		mu.Unlock()
		if needsWrite {
			writeCache(written)
		}
		close(ch)
	}()
	return ch
}

// cachedOrProbe returns a memoised candidate when there is a fresh one, and
// otherwise probes. The bool reports whether the memo was used.
func cachedOrProbe(ctx context.Context, b Backend, file cacheFile, ttl time.Duration) (Candidate, bool) {
	if d, ok := file.lookup(b, ttl); ok {
		return d.toCandidate(b), true
	}
	return probe(ctx, b), false
}
