package reddit

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	defaultBurst      = 5
	rateCeilingFactor = 2.0
	rateFloorFactor   = 0.1
)

type limiter struct {
	logger *slog.Logger

	inner         *rate.Limiter
	sustainedRate rate.Limit

	mu      sync.Mutex
	barrier time.Time
}

func newLimiter(rpm int, logger *slog.Logger) *limiter {
	sustained := rate.Limit(float64(rpm) / 60.0)

	return &limiter{
		logger:        logger,
		inner:         rate.NewLimiter(sustained, defaultBurst),
		sustainedRate: sustained,
	}
}

func (l *limiter) wait(ctx context.Context) error {
	if err := l.waitBarrier(ctx); err != nil {
		return err
	}

	if err := l.inner.Wait(ctx); err != nil {
		return fmt.Errorf("rate wait: %w", err)
	}

	return nil
}

func (l *limiter) waitBarrier(ctx context.Context) error {
	l.mu.Lock()
	until := l.barrier
	l.mu.Unlock()

	if until.IsZero() {
		return nil
	}

	delay := time.Until(until)
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("ctx: %w", ctx.Err())
	}
}

func (l *limiter) observe(headers http.Header, retryAfter time.Duration) {
	l.applyHeaders(headers)

	if retryAfter > 0 {
		l.extendBarrier(time.Now().Add(retryAfter))
	}
}

func (l *limiter) applyHeaders(headers http.Header) {
	remainingHeader := headers.Get("X-Ratelimit-Remaining")
	resetHeader := headers.Get("X-Ratelimit-Reset")

	if remainingHeader == "" || resetHeader == "" {
		return
	}

	remaining, err := strconv.ParseFloat(remainingHeader, 64)
	if err != nil {
		return
	}

	resetSeconds, err := strconv.Atoi(resetHeader)
	if err != nil || resetSeconds <= 0 {
		l.logger.Debug("reddit rate limit reset header invalid", "reset_header", resetHeader)

		return
	}

	if remaining <= 0 {
		l.extendBarrier(time.Now().Add(time.Duration(resetSeconds) * time.Second))
		l.logger.Debug("reddit rate limit exhausted",
			"reset_seconds", resetSeconds)

		return
	}

	target := rate.Limit(remaining / float64(resetSeconds))

	ceiling := l.sustainedRate * rateCeilingFactor
	floor := l.sustainedRate * rateFloorFactor

	if target > ceiling {
		target = ceiling
	}

	if target < floor {
		target = floor
	}

	l.inner.SetLimit(target)
}

func (l *limiter) extendBarrier(until time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if until.After(l.barrier) {
		l.barrier = until
	}
}
