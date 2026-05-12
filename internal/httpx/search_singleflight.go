package httpx

import "sync"

type searchSingleFlight struct {
	mu    sync.Mutex
	calls map[string]*searchCall
}

type searchCall struct {
	wg    sync.WaitGroup
	val   SearchPageData
	panic any
}

func newSearchSingleFlight() *searchSingleFlight {
	return &searchSingleFlight{
		calls: make(map[string]*searchCall),
	}
}

func (g *searchSingleFlight) do(key string, fn func() SearchPageData) SearchPageData {
	if g == nil || key == "" {
		return fn()
	}
	g.mu.Lock()
	if call, ok := g.calls[key]; ok {
		g.mu.Unlock()
		call.wg.Wait()
		if call.panic != nil {
			panic(call.panic)
		}
		return call.val
	}
	call := &searchCall{}
	call.wg.Add(1)
	g.calls[key] = call
	g.mu.Unlock()

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				call.panic = recovered
			}
			call.wg.Done()
			g.mu.Lock()
			delete(g.calls, key)
			g.mu.Unlock()
		}()
		call.val = fn()
	}()
	if call.panic != nil {
		panic(call.panic)
	}
	return call.val
}
