package httpx

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter is a token bucket per client address, in memory. Each replica counts separately,
// which is enough to stop password guessing at the rates that matter for a small app.
type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*client
	limit   rate.Limit
	burst   int
	now     func() time.Time
}

type client struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewRateLimiter allows perMinute requests a minute per client, in bursts of up to perMinute.
func NewRateLimiter(perMinute int) *RateLimiter {
	return &RateLimiter{
		clients: map[string]*client{},
		limit:   rate.Limit(float64(perMinute) / 60),
		burst:   perMinute,
		now:     time.Now,
	}
}

// Allow reports whether key may make a request now, and if not, how long until it may.
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	c, ok := l.clients[key]
	if !ok {
		if len(l.clients) > 10_000 {
			l.sweep(now)
		}
		c = &client{lim: rate.NewLimiter(l.limit, l.burst)}
		l.clients[key] = c
	}
	c.seen = now
	r := c.lim.ReserveN(now, 1)
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return false, d
	}
	return true, 0
}

// sweep forgets clients idle long enough for their bucket to have refilled.
func (l *RateLimiter) sweep(now time.Time) {
	idle := time.Duration(float64(l.burst)/float64(l.limit)) * time.Second
	for k, c := range l.clients {
		if now.Sub(c.seen) > idle {
			delete(l.clients, k)
		}
	}
}

// Middleware rejects requests over the limit with 429 and Retry-After.
func (l *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := l.Allow(ClientIPFrom(r.Context())); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
			WriteError(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again shortly.")
			return
		}
		next.ServeHTTP(w, r)
	})
}
