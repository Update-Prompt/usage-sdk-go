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
	client.sendMu.Lock()
	defer client.sendMu.Unlock()
	for {
		client.mu.Lock()
		if len(client.queue) == 0 {
			client.mu.Unlock()
			client.retryAt = time.Time{}
			return nil
		}
		head := client.queue[0]
		client.mu.Unlock()

		if delay := client.retryAt.Sub(client.now()); delay > 0 {
			if !wait {
				return nil
			}
			if !sleep(ctx, delay) {
				return ErrNotSent
			}
		}
		if ctx.Err() != nil {
			return ErrNotSent
		}

		retryAfter, retry := client.send(ctx, head)
		if retry {
			head.tries++
			client.retryAt = client.now().Add(client.retryDelay(head.tries, retryAfter))
			continue
		}
		client.retryAt = time.Time{}
		client.mu.Lock()
		client.queue = client.queue[1:]
		client.mu.Unlock()
	}
}

// send makes one request with a batch. It returns whether to retry the batch
// and, if the API said so, after how long.
func (client *Client) send(ctx context.Context, b *batch) (retryAfter time.Duration, retry bool) {
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
		return 0, false
	}
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", b.idempotencyKey)
	request.Header.Set("User-Agent", client.userAgent)

	response, err := client.httpClient.Do(request)
	if err != nil {
		// The error names the URL, never the key
		client.logOnce("can't reach the Usage API, retrying", "error", err)
		return 0, true
	}
	defer response.Body.Close()
	// Read a little, so the connection can be reused
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))

	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		client.sent.Add(b.calls)
		return 0, false
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		client.logOnce(fmt.Sprintf("the Usage API answered %d, retrying", response.StatusCode))
		return parseRetryAfter(response.Header.Get("Retry-After"), client.now()), true
	default:
		client.dropped.Add(b.calls)
		client.logOnce(fmt.Sprintf("the Usage API refused a batch with %d, so its calls are dropped", response.StatusCode),
			"calls", b.calls)
		return 0, false
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

// parseRetryAfter reads a Retry-After header in seconds or as an HTTP date.
// Zero means it's missing or invalid.
func parseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return max(time.Duration(seconds)*time.Second, 0)
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(at.Sub(now), 0)
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
