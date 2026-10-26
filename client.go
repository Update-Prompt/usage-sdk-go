package updateprompt

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Version is this SDK's version, sent in the User-Agent header
const Version = "0.1.0"

// DefaultBaseURL is the address of the Update Prompt Usage API
const DefaultBaseURL = "https://usage.updateprompt.com"

const (
	// sendInterval is how often counts are sent, give or take jitter
	sendInterval = 10 * time.Second
	// maxPendingReports caps the distinct reports kept in memory, counting and
	// waiting to be sent together
	maxPendingReports = 10_000
	// requestTimeout is how long one request to the Usage API may take
	requestTimeout = 10 * time.Second
)

// Options configure a [Client]. Every field is optional.
type Options struct {
	// APIKey is the usage API key of your API in Update Prompt. Empty reads
	// UPDATE_PROMPT_API_KEY. Without a key the client counts nothing.
	APIKey string
	// BaseURL is the Usage API's address. Empty reads UPDATE_PROMPT_URL, then
	// uses [DefaultBaseURL].
	BaseURL string
	// Consumer returns the id of the consumer making a request, such as a
	// client id from a header. It runs after your handler, and only when
	// [SetConsumer] wasn't called for the request. It gets the request as the
	// middleware got it, so values that code below the middleware added to
	// the request's context aren't there: use [SetConsumer] for those. Empty
	// means no consumer id.
	Consumer func(*http.Request) string
	// Route returns the route template of a request, such as /orders/{id},
	// for routers the middleware can't read. It runs after your handler.
	// Empty falls back to the router's pattern, then to the raw path.
	Route func(*http.Request) string
	// Logger gets one line per problem, such as a refused API key. Nil logs
	// nothing.
	Logger *slog.Logger
}

// Stats are a client's counts since it started
type Stats struct {
	// Counted is the calls recorded
	Counted uint64
	// Sent is the calls the Usage API accepted
	Sent uint64
	// Dropped is the calls given up: over the memory limit, refused by the
	// Usage API, recorded after Close, or still unsent when Close ran out of
	// time
	Dropped uint64
}

// Client counts calls and sends them to the Usage API in the background.
// Its methods are safe for concurrent use.
type Client struct {
	apiKey     string
	baseURL    string
	consumer   func(*http.Request) string
	route      func(*http.Request) string
	logger     *slog.Logger
	httpClient *http.Client
	userAgent  string

	// interval and backoff are fields so tests can shorten them
	interval   time.Duration
	minBackoff time.Duration
	maxBackoff time.Duration
	now        func() time.Time

	// mu guards counts, queue, queuedReports and closed
	mu     sync.Mutex
	counts map[reportKey]*tally
	queue  []*batch
	// queuedReports is how many reports are queued or being made into batches
	queuedReports int
	closed        bool

	// sendSlot is held by whoever sends, so at most one request is in flight.
	// Only its holder changes the head of the queue.
	sendSlot chan struct{}
	// retryAt is when the head of the queue may be tried again, in Unix
	// nanoseconds, or 0
	retryAt atomic.Int64

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}

	counted atomic.Uint64
	sent    atomic.Uint64
	dropped atomic.Uint64

	logged sync.Map // problem → time it was last logged
}

// New returns a client and starts sending in the background. Call
// [Client.Close] on shutdown to send what's left.
func New(options Options) *Client {
	client := newClient(options)
	if client.apiKey != "" {
		go client.run()
	} else {
		close(client.done)
		client.logger.Warn("Update Prompt: no API key, so calls are not reported. Set UPDATE_PROMPT_API_KEY or Options.APIKey.")
	}
	return client
}

// newClient returns a client without starting it
func newClient(options Options) *Client {
	apiKey := options.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("UPDATE_PROMPT_API_KEY")
	}
	baseURL := options.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("UPDATE_PROMPT_URL")
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Client{
		apiKey:     strings.TrimSpace(apiKey),
		baseURL:    strings.TrimRight(baseURL, "/"),
		consumer:   options.Consumer,
		route:      options.Route,
		logger:     logger,
		httpClient: &http.Client{Timeout: requestTimeout},
		userAgent:  userAgent(),
		interval:   sendInterval,
		minBackoff: time.Second,
		maxBackoff: time.Minute,
		now:        time.Now,
		counts:     map[reportKey]*tally{},
		sendSlot:   make(chan struct{}, 1),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// Stats returns the client's counts since it started
func (client *Client) Stats() Stats {
	return Stats{
		Counted: client.counted.Load(),
		Sent:    client.sent.Load(),
		Dropped: client.dropped.Load(),
	}
}

// Flush sends every call counted so far and waits until the Usage API has
// answered, retrying until ctx is done. Use it at the end of a serverless
// invocation, where the process may be frozen before the next send.
func (client *Client) Flush(ctx context.Context) error {
	client.queueCounts()
	return client.sendQueue(ctx, true)
}

// Close stops the background sending, then sends what's left until ctx is
// done. Calls that are still unsent are dropped, and so are calls recorded
// after Close.
func (client *Client) Close(ctx context.Context) error {
	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()
	client.stopOnce.Do(func() { close(client.stop) })
	select {
	case <-client.done:
	case <-ctx.Done():
	}
	var err error
	if client.apiKey != "" {
		err = client.Flush(ctx)
	}

	client.mu.Lock()
	var unsent uint64
	for _, t := range client.counts {
		unsent += t.calls
	}
	client.counts = map[reportKey]*tally{}
	client.mu.Unlock()
	// The queue only changes in the hands of the sender, and someone else's
	// Flush may still be sending: then its batches are left to it
	select {
	case client.sendSlot <- struct{}{}:
		client.mu.Lock()
		for _, b := range client.queue {
			unsent += b.calls
			client.queuedReports -= b.reports
		}
		client.queue = nil
		client.mu.Unlock()
		<-client.sendSlot
	default:
	}
	client.dropped.Add(unsent)
	return err
}

// run sends counts every interval until Close
func (client *Client) run() {
	defer close(client.done)
	// Ends requests in flight when Close is called; their batches stay queued
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-client.stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	nextSend := client.now().Add(jitter(client.interval))
	for {
		wait := nextSend.Sub(client.now())
		if retryAt := client.retryAt.Load(); retryAt != 0 {
			wait = min(wait, time.Unix(0, retryAt).Sub(client.now()))
		}
		timer := time.NewTimer(max(wait, 0))
		select {
		case <-client.stop:
			timer.Stop()
			return
		case <-timer.C:
		}

		if !client.now().Before(nextSend) {
			client.queueCounts()
			nextSend = client.now().Add(jitter(client.interval))
		}
		_ = client.sendQueue(ctx, false)
	}
}

// logOnce logs a problem at most once an hour
func (client *Client) logOnce(problem string, args ...any) {
	now := client.now()
	if last, ok := client.logged.Load(problem); ok && now.Sub(last.(time.Time)) < time.Hour {
		return
	}
	client.logged.Store(problem, now)
	client.logger.Warn("Update Prompt: "+problem, args...)
}
