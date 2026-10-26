package updateprompt

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"runtime"
	"strconv"
	"time"
)

// ErrNotSent means Flush or Close ran out of time with calls still unsent
var ErrNotSent = errors.New("updateprompt: calls not sent before the deadline")

// sendQueue sends the queued batches in order, one request at a time. When a
// batch must be retried, it waits and retries if wait is set, else it notes
// when to retry and returns.
func (client *Client) sendQueue(ctx context.Context, wait bool) error {
	select {
	case client.sendSlot <- struct{}{}:
	case <-ctx.Done():
		return ErrNotSent
	}
	defer func() { <-client.sendSlot }()
	for {
		client.mu.Lock()
		if len(client.queue) == 0 {
			client.mu.Unlock()
			client.retryAt.Store(0)
			return nil
		}
		head := client.queue[0]
		client.mu.Unlock()

		if retryAt := client.retryAt.Load(); retryAt != 0 {
			if delay := time.Unix(0, retryAt).Sub(client.now()); delay > 0 {
				if !wait {
					return nil
				}
				if !sleep(ctx, delay) {
					return ErrNotSent
				}
			}
		}
		if ctx.Err() != nil {
			return ErrNotSent
		}

		retryAfter, outcome := client.send(ctx, head)
		switch outcome {
		case aborted:
			return ErrNotSent
		case retry:
			head.tries++
			client.retryAt.Store(client.now().Add(client.retryDelay(head.tries, retryAfter)).UnixNano())
			continue
		}
		client.retryAt.Store(0)
		client.mu.Lock()
		if len(client.queue) > 0 && client.queue[0] == head {
			client.queue = client.queue[1:]
			client.queuedReports -= head.reports
		}
		client.mu.Unlock()
	}
}

// sendOutcome is what became of one try to send a batch
type sendOutcome int

const (
	// finished: the batch was accepted or refused, and is done with
	finished sendOutcome = iota
	// retry: the batch must be tried again later
	retry
	// aborted: ctx ended the try, which says nothing about the API
	aborted
)

// send makes one request with a batch. With retry, it also returns how long
// the API asked to wait, if it did.
func (client *Client) send(ctx context.Context, b *batch) (time.Duration, sendOutcome) {
	if b.idempotencyKey == "" {
		b.idempotencyKey = newIdempotencyKey()
	}
	path := "/v1/usage"
	if b.kind == pathReport {
		path = "/v1/usage/paths"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+path, bytes.NewReader(b.body))
	if err != nil {
		// Only a malformed base URL gets here, and it won't get better
		client.dropped.Add(b.calls)
		client.logOnce("can't send usage: invalid base URL", "error", err)
		return 0, finished
	}
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", b.idempotencyKey)
	request.Header.Set("User-Agent", client.userAgent)

	response, err := client.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, aborted
		}
		// The error names the URL, never the key
		client.logOnce("can't reach the Usage API, retrying", "error", err)
		return 0, retry
	}
	defer response.Body.Close()
	// Read a little, so the connection can be reused
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))

	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		client.sent.Add(b.calls)
		return 0, finished
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		client.logOnce(fmt.Sprintf("the Usage API answered %d, retrying", response.StatusCode))
		return parseRetryAfter(response.Header.Get("Retry-After"), client.now()), retry
	default:
		client.dropped.Add(b.calls)
		client.logOnce(fmt.Sprintf("the Usage API refused a batch with %d, so its calls are dropped", response.StatusCode),
			"calls", b.calls)
		return 0, finished
	}
}

// retryDelay is how long to wait before a batch's next try: what the API asked
// for, else a backoff that doubles with each try, both with jitter
func (client *Client) retryDelay(tries int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		// Never earlier than asked, so jitter only adds
		return retryAfter + time.Duration(rand.Int64N(int64(retryAfter/5)+1))
	}
	delay := client.minBackoff
	for i := 1; i < tries && delay < client.maxBackoff; i++ {
		delay *= 2
	}
	return jitter(min(delay, client.maxBackoff))
}

// maxRetryAfter caps the wait a Retry-After asks for, so a wrong header can't
// hold back sending for long. The API asks for a minute at most.
const maxRetryAfter = 5 * time.Minute

// parseRetryAfter reads a Retry-After header in seconds or as an HTTP date, up
// to maxRetryAfter. Zero means it's missing or invalid.
func parseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds > int64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return max(time.Duration(seconds)*time.Second, 0)
	}
	if at, err := http.ParseTime(value); err == nil {
		return min(max(at.Sub(now), 0), maxRetryAfter)
	}
	return 0
}

// jitter returns d changed by up to 20% either way, so many instances don't
// send in step
func jitter(d time.Duration) time.Duration {
	spread := int64(d) * 2 / 5
	if spread <= 0 {
		return d
	}
	return d - time.Duration(spread/2) + time.Duration(rand.Int64N(spread+1))
}

// sleep waits for d or until ctx is done, and reports whether it waited the
// whole time
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// newIdempotencyKey returns 128 random bits, base64url encoded
func newIdempotencyKey() string {
	key := make([]byte, 16)
	_, _ = cryptorand.Read(key)
	return base64.RawURLEncoding.EncodeToString(key)
}

// userAgent names this SDK and the Go version
func userAgent() string {
	return "usage-sdk-go/" + Version + " (" + runtime.Version() + ")"
}
