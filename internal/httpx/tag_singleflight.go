package httpx

import "sync"

type tagSingleFlight struct {
	mu    sync.Mutex
	calls map[string]*tagCall
}

type tagCall struct {
	wg    sync.WaitGroup
	val   TagPageData
	panic any
}

func newTagSingleFlight() *tagSingleFlight {
	return &tagSingleFlight{
		calls: make(map[string]*tagCall),
	}
}

func (g *tagSingleFlight) do(key string, fn func() TagPageData) TagPageData {
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
	call := &tagCall{}
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
