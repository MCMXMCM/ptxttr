package httpx

// warmAnonymousThreadMiss schedules cache hydration for a public thread document
// miss without making the anonymous request wait on relay I/O.
func (s *Server) warmAnonymousThreadMiss(eventID string, relays []string) {
	if s == nil || eventID == "" {
		return
	}
	s.metrics.Add("thread.anonymous_miss.cache_only", 1)
}
